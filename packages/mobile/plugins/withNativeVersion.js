const { withInfoPlist, withXcodeProject } = require("@expo/config-plugins");

// Expo's prebuild hardcodes the version into Info.plist and leaves the Xcode build
// settings at the template defaults (1.0 / 1), so Xcode's General tab disagrees with
// the app and has no Display Name. Make the build settings the source (from
// app.json version / ios.buildNumber) and have Info.plist reference them.
function withNativeVersion(config) {
	const version = config.ios?.version ?? config.version ?? "1.0.0";
	const build = String(config.ios?.buildNumber ?? "1");
	config = withXcodeProject(config, (cfg) => {
		const project = cfg.modResults;
		project.updateBuildProperty("MARKETING_VERSION", version);
		project.updateBuildProperty("CURRENT_PROJECT_VERSION", build);
		return cfg;
	});
	return withInfoPlist(config, (cfg) => {
		cfg.modResults.CFBundleShortVersionString = "$(MARKETING_VERSION)";
		cfg.modResults.CFBundleVersion = "$(CURRENT_PROJECT_VERSION)";
		cfg.modResults.CFBundleDisplayName = config.name;
		return cfg;
	});
}

module.exports = withNativeVersion;
