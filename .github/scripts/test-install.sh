#!/bin/sh
# Runs install.sh as `curl … | sh` would: a release pinned without its v,
# then the latest over it. A lopper earlier in PATH must be warned about.
set -eu
export LOPPER_INSTALL_DIR="$RUNNER_TEMP/bin"

LOPPER_VERSION=0.1.0-rc.1 sh install.sh
version=$("$LOPPER_INSTALL_DIR/lopper" --version)
case "$version" in
*" 0.1.0-rc.1 "*) ;;
*) echo "LOPPER_VERSION=0.1.0-rc.1 installed $version" && exit 1 ;;
esac

mkdir -p "$RUNNER_TEMP/other"
printf '#!/bin/sh\n' >"$RUNNER_TEMP/other/lopper"
chmod +x "$RUNNER_TEMP/other/lopper"
PATH="$RUNNER_TEMP/other:$PATH" sh install.sh >"$RUNNER_TEMP/out"
cat "$RUNNER_TEMP/out"

version=$("$LOPPER_INSTALL_DIR/lopper" --version)
case "$version" in
*rc.1*) echo "the latest release did not replace $version" && exit 1 ;;
esac
grep -qF "$RUNNER_TEMP/other/lopper comes first in your PATH" "$RUNNER_TEMP/out" ||
	{ echo "no warning about $RUNNER_TEMP/other/lopper" && exit 1; }
test "$(ls -A "$LOPPER_INSTALL_DIR")" = lopper || { echo "a staging file is left" && exit 1; }
