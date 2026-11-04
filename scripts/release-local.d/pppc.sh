# shellcheck shell=bash
# Sourced by scripts/release-local (never run directly): `pppc <app>` builds the
# Jamf/MDM Configuration Profile that pre-approves Documents, Desktop and
# Downloads folder access for the app and its daemon, and registers Tallpine's
# login item / background daemon (com.apple.servicemanagement) by Team ID.
# Everything comes from the signed app's codesign designated requirements; no
# Team ID is committed. Refuses ad hoc, unsigned and Team-ID-less builds, whose
# requirements would not match a release.

pppc_services=(SystemPolicyDocumentsFolder SystemPolicyDesktopFolder SystemPolicyDownloadsFolder)

xml_escape() { local s="$1"; s="${s//&/&amp;}"; s="${s//</&lt;}"; printf '%s' "${s//>/&gt;}"; }

# pppc_requirement <path>: print the designated requirement, die unless it is a
# Developer ID one (leaf[subject.OU] present, no cdhash pin).
pppc_requirement() {
  local path="$1" out req
  out="$(codesign_cmd -d -r- "${path}" 2>&1 || true)"
  req="$(printf '%s\n' "${out}" | sed -n 's/^designated => //p' | head -n 1)"
  [[ -n "${req}" ]] || die "${path} is unsigned: codesign reports no designated requirement"
  [[ "${req}" != *cdhash* && "${req}" == *"certificate leaf[subject.OU] = "* ]] ||
    die "${path} is ad hoc signed or has no Team ID in its requirement (${req}); build with a Developer ID identity"
  printf '%s\n' "${req}"
}

# pppc_identifier <requirement>: the quoted signing identifier of a requirement.
pppc_identifier() {
  local id
  id="$(printf '%s\n' "$1" | sed -n 's/.*identifier "\([^"]*\)".*/\1/p' | head -n 1)"
  [[ -n "${id}" ]] || die "no identifier in requirement: $1"
  printf '%s\n' "${id}"
}

pppc_team() {
  local team
  team="$(printf '%s\n' "$1" | sed -n 's/.*leaf\[subject.OU\] = "\{0,1\}\([A-Z0-9]\{10\}\)"\{0,1\}.*/\1/p' | head -n 1)"
  [[ -n "${team}" ]] || die "no Team ID in requirement: $1"
  printf '%s\n' "${team}"
}

# pppc_entry <identifier> <requirement>: one Services array item.
pppc_entry() {
  printf '\t\t\t\t\t\t<dict>\n\t\t\t\t\t\t\t<key>Identifier</key>\n\t\t\t\t\t\t\t<string>%s</string>\n' "$(xml_escape "$1")"
  printf '\t\t\t\t\t\t\t<key>IdentifierType</key>\n\t\t\t\t\t\t\t<string>bundleID</string>\n'
  printf '\t\t\t\t\t\t\t<key>CodeRequirement</key>\n\t\t\t\t\t\t\t<string>%s</string>\n' "$(xml_escape "$2")"
  printf '\t\t\t\t\t\t\t<key>Authorization</key>\n\t\t\t\t\t\t\t<string>Allow</string>\n\t\t\t\t\t\t</dict>\n'
}

# pppc_profile <app>: print the Configuration Profile.
pppc_profile() {
  local app="$1" daemon app_req dmn_req team dmn_team svc
  daemon="${app}/Contents/Resources/daemon/${cli_name}"
  [[ -e "${daemon}" ]] || die "daemon missing from the app: ${daemon}"
  app_req="$(pppc_requirement "${app}")" || exit 1
  dmn_req="$(pppc_requirement "${daemon}")" || exit 1
  team="$(pppc_team "${app_req}")" || exit 1
  dmn_team="$(pppc_team "${dmn_req}")" || exit 1
  [[ "${team}" == "${dmn_team}" ]] || die "the app (${team}) and its daemon (${dmn_team}) are signed by different teams"
  local app_id dmn_id
  app_id="$(pppc_identifier "${app_req}")" || exit 1
  dmn_id="$(pppc_identifier "${dmn_req}")" || exit 1
  cat <<EOP
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.TCC.configuration-profile-policy</string>
			<key>PayloadIdentifier</key>
			<string>ee.justdev.tallpine.pppc.tcc</string>
			<key>PayloadUUID</key>
			<string>$(uuidgen)</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>PayloadDisplayName</key>
			<string>${product_name} folder access</string>
			<key>Services</key>
			<dict>
EOP
  for svc in "${pppc_services[@]}"; do
    printf '\t\t\t\t<key>%s</key>\n\t\t\t\t<array>\n' "${svc}"
    pppc_entry "${app_id}" "${app_req}"
    pppc_entry "${dmn_id}" "${dmn_req}"
    printf '\t\t\t\t</array>\n'
  done
  cat <<EOP
			</dict>
		</dict>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.servicemanagement</string>
			<key>PayloadIdentifier</key>
			<string>ee.justdev.tallpine.pppc.login-items</string>
			<key>PayloadUUID</key>
			<string>$(uuidgen)</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>PayloadDisplayName</key>
			<string>${product_name} background items</string>
			<key>Rules</key>
			<array>
				<dict>
					<key>RuleType</key>
					<string>TeamIdentifier</string>
					<key>RuleValue</key>
					<string>${team}</string>
					<key>Comment</key>
					<string>${product_name} app and daemon</string>
				</dict>
			</array>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>${product_name} privacy and background items</string>
	<key>PayloadIdentifier</key>
	<string>ee.justdev.tallpine.pppc</string>
	<key>PayloadOrganization</key>
	<string>${product_name}</string>
	<key>PayloadScope</key>
	<string>System</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>$(uuidgen)</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
EOP
}

# cmd_pppc <app> [--out <file>]: write the profile (default ./Tallpine-PPPC.mobileconfig).
cmd_pppc() {
  local app="${1:-}" out="${opt_out:-./${product_name}-PPPC.mobileconfig}" tmp
  [[ -n "${app}" && -d "${app}" ]] || die "usage: release-local pppc <app> [--out <file>]"
  app="$(cd "${app}" && pwd -P)"
  [[ "$(plist_get "${app}" CFBundleIdentifier)" == "${bundle_id}" ]] || die "${app} is not a ${product_name} build (bundle id)"
  verify_signature "${app}" || die "${app} is unsigned, ad hoc signed or fails verification; the profile needs a Developer ID build"
  tmp="$(mktemp "${TMPDIR:-/tmp}/tallpine-pppc.XXXXXX")"
  if ! (pppc_profile "${app}") >"${tmp}"; then rm -f "${tmp}"; exit 1; fi
  if command -v plutil >/dev/null 2>&1; then
    plutil -lint "${tmp}" >/dev/null || { rm -f "${tmp}"; die "generated profile failed plutil -lint"; }
  fi
  mv "${tmp}" "${out}"
  say "Wrote ${out}"
}
