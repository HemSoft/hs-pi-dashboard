"""Cross-compile and validate fleet binaries before deployment."""
import argparse
import os
from pathlib import Path
import struct
import subprocess

TARGETS = {'windows-amd64': ('windows', 'amd64'),
           'linux-amd64': ('linux', 'amd64'),
           'darwin-arm64': ('darwin', 'arm64')}


def validate_binary(path, target):
    with open(path, 'rb') as binary:
        header = binary.read(64)
        valid = False
        if target == 'linux-amd64':
            valid = (len(header) == 64 and header[:6] == b'\x7fELF\x02\x01'
                     and struct.unpack_from('<H', header, 18)[0] == 62)
        elif target == 'darwin-arm64':
            valid = (len(header) == 64 and header[:4] == b'\xcf\xfa\xed\xfe'
                     and struct.unpack_from('<I', header, 4)[0] == 0x0100000C)
        elif target == 'windows-amd64':
            if len(header) == 64 and header[:2] == b'MZ':
                binary.seek(struct.unpack_from('<I', header, 60)[0])
                pe = binary.read(6)
                valid = pe == b'PE\0\0\x64\x86'
        if not valid:
            raise ValueError(f'{path}: binary does not match {target}; refusing deployment')


def build(target, output_dir):
    goos, goarch = TARGETS[target]
    destination = output_dir / f'hs-pi-dashboard-{target}{".exe" if goos == "windows" else ""}'
    temporary = destination.with_suffix(destination.suffix + '.building')
    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
    startup = None
    flags = 0
    if os.name == 'nt':
        startup = subprocess.STARTUPINFO()
        startup.dwFlags |= subprocess.STARTF_USESHOWWINDOW
        startup.wShowWindow = 0
        flags = subprocess.CREATE_NO_WINDOW
    try:
        subprocess.run(['go', 'build', '-o', str(temporary), '.'],
                       cwd=Path(__file__).resolve().parent.parent, env=env,
                       startupinfo=startup, creationflags=flags,
                       check=True, timeout=180)
        validate_binary(temporary, target)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)
    print(f'Validated {target}: {destination}')
    return destination


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', choices=['all', *TARGETS], default='all')
    args = parser.parse_args()
    output = Path(__file__).resolve().parent.parent / 'dist'
    output.mkdir(exist_ok=True)
    for selected in TARGETS if args.target == 'all' else [args.target]:
        build(selected, output)
