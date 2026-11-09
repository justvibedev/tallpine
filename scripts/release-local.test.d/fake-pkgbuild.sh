#!/bin/bash
echo "pkgbuild $*" >>"${T}/pkg.log"
echo "write: Permission denied" >&2; echo "write: Permission denied" >&2
if [[ -f "${T}/pkgbuild-fail" ]]; then echo "pkgbuild: error: real failure" >&2; exit 1; fi
if [[ "$1" == --analyze ]]; then printf '<?xml version="1.0"?><plist version="1.0"><array><dict><key>BundleIsRelocatable</key><true/></dict></array></plist>' >"${@: -1}"; exit 0; fi
for a in "$@"; do [[ "$prev" == --component-plist ]] && plutil -extract 0.BundleIsRelocatable raw "$a" >>"${T}/reloc"; prev="$a"; done
: >"${@: -1}"
