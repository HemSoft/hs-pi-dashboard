import { existsSync, readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const repositoryRoot = realpathSync(fileURLToPath(new URL("../../", import.meta.url)));
const playbackScript = join(repositoryRoot, "scripts", "Play-DoneSound.ps1");
const configPath = join(repositoryRoot, ".pi", "done-sound.json");

// Check real paths and stop at a nested repository boundary.
export function belongsToRepository(cwd: string): boolean {
	try {
		let directory = realpathSync(cwd);
		while (true) {
			if (directory === repositoryRoot) return true;
			if (existsSync(join(directory, ".git"))) return false;
			const parent = dirname(directory);
			if (parent === directory) return false;
			directory = parent;
		}
	} catch {
		return false;
	}
}

export default function doneSound(pi: ExtensionAPI) {
	let playing = false;

	// Unlike agent_end, this runs only after retries and queued follow-ups finish.
	pi.on("agent_settled", async (_event, ctx) => {
		if (!ctx.hasUI || process.env.GENERATE_AUDIO_DONE_SOUND === "0" ||
			playing || !belongsToRepository(ctx.cwd)) return;

		playing = true;
		try {
			const config = JSON.parse(readFileSync(configPath, "utf8"));
			if (config.version !== 1 || config.managedBy !== "generate-audio" || typeof config.audioPath !== "string") {
				throw new Error("Invalid completion audio configuration");
			}
			if (config.enabled === false) return;
			const result = await pi.exec("pwsh", ["-NoProfile", "-File", playbackScript, "-AudioPath", config.audioPath], {
				cwd: repositoryRoot,
				timeout: 15000,
			});
			if (result.code !== 0 || result.killed) {
				ctx.ui.notify("Completion audio could not play. Check ffplay and assets/done.mp3.", "warning");
			}
		} catch {
			ctx.ui.notify("Completion audio could not start. Check PowerShell and the audio player.", "warning");
		} finally {
			playing = false;
		}
	});
}
