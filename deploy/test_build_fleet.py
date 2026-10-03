import pathlib
import struct
import tempfile
import unittest
from build_fleet import validate_binary


def elf(machine=62):
    header = bytearray(64)
    header[:6] = b'\x7fELF\x02\x01'
    struct.pack_into('<H', header, 18, machine)
    return header


def pe():
    header = bytearray(134)
    header[:2] = b'MZ'
    struct.pack_into('<I', header, 60, 128)
    header[128:134] = b'PE\0\0\x64\x86'
    return header


def macho(cpu=0x0100000C):
    header = bytearray(64)
    header[:4] = b'\xcf\xfa\xed\xfe'
    struct.pack_into('<I', header, 4, cpu)
    return header


class BinaryTargetTests(unittest.TestCase):
    def check_header(self, payload, target, valid):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'hs-pi-dashboard-linux-amd64'
            path.write_bytes(payload)
            if valid:
                validate_binary(path, target)
            else:
                with self.assertRaises(ValueError):
                    validate_binary(path, target)

    def test_filename_cannot_disguise_windows_binary_as_linux(self):
        self.check_header(pe(), 'linux-amd64', False)

    def test_linux_requires_x86_64(self):
        self.check_header(elf(183), 'linux-amd64', False)

    def test_macos_requires_arm64(self):
        self.check_header(macho(0x01000007), 'darwin-arm64', False)

    def test_rejects_truncated_and_unknown_headers(self):
        for payload in [b'', b'MZ', b'\x7fELF', bytes(64)]:
            for target in ['linux-amd64', 'windows-amd64', 'darwin-arm64']:
                with self.subTest(target=target, payload=payload[:4]):
                    self.check_header(payload, target, False)

    def test_accepts_matching_formats(self):
        for payload, target in [(elf(), 'linux-amd64'), (pe(), 'windows-amd64'), (macho(), 'darwin-arm64')]:
            with self.subTest(target=target):
                self.check_header(payload, target, True)


if __name__ == '__main__':
    unittest.main()
