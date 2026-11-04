const fs = require("fs");
const path = require("path");
const { withDangerousMod } = require("@expo/config-plugins");

// Expo's generated scheme runs Debug, which expects a Metro server and shows "No
// script URL" when Xcode launches the app on its own. Default the Run action to Release.
function setLaunchActionRelease(xml) {
	return xml.replace(/(<LaunchAction\b[^>]*?\bbuildConfiguration = )"[^"]*"/, '$1"Release"');
}

function withReleaseScheme(config) {
	return withDangerousMod(config, [
		"ios",
		(cfg) => {
			const dir = path.join(cfg.modRequest.platformProjectRoot, `${cfg.modRequest.projectName}.xcodeproj`, "xcshareddata", "xcschemes");
			if (fs.existsSync(dir)) {
				for (const file of fs.readdirSync(dir).filter((f) => f.endsWith(".xcscheme"))) {
					const p = path.join(dir, file);
					fs.writeFileSync(p, setLaunchActionRelease(fs.readFileSync(p, "utf8")));
				}
			}
			return cfg;
		},
	]);
}

module.exports = withReleaseScheme;
module.exports.setLaunchActionRelease = setLaunchActionRelease;
