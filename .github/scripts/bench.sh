#!/bin/sh
# Times `lopper scan` built from a base commit and from this checkout on the
# same generated repositories, and fails when a scan is slower by more than
# BENCH_MAX_SLOWDOWN percent. The two builds take turns, so a noisy runner
# slows both alike.
set -eu
base=${1:-main}
rounds=${BENCH_ROUNDS:-10}
max_slowdown=${BENCH_MAX_SLOWDOWN:-20}

root=$(git rev-parse --show-toplevel)
cd "$root/apps/lopper"
set -- cmd internal go.mod go.sum "$root/.github/scripts/bench.sh"
if git diff --quiet "$base" -- "$@" && [ -z "$(git ls-files --others --exclude-standard -- "$@")" ]; then
	echo "lopper is the same as in $base: nothing to compare"
	exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/base"
git -C "$root" archive "$base" apps/lopper | tar -x -C "$work/base"
(cd "$work/base/apps/lopper" && go build -o "$work/lopper-base" ./cmd/lopper)
go build -o "$work/lopper-head" ./cmd/lopper
go test -c -o "$work/bench.test" ./cmd/lopper

export LOPPER_BENCH_FIXTURE="$work/fixture"
bench() {
	LOPPER_BENCH_BIN="$work/lopper-$1" "$work/bench.test" -test.run '^$' -test.bench . -test.benchtime 1x
}
# The first scans fill the file system caches and are not counted.
bench base >/dev/null
bench head >/dev/null
i=0
while [ "$i" -lt "$rounds" ]; do
	bench base >>"$work/base.txt"
	bench head >>"$work/head.txt"
	i=$((i + 1))
done

benchstat() {
	go tool -modfile tools/go.mod benchstat "$@" base="$work/base.txt" head="$work/head.txt"
}
benchstat | tee "$work/benchstat.txt"

# benchstat prints a change only when it is significant, and "~" otherwise.
benchstat -format csv 2>/dev/null | awk -F, -v max="$max_slowdown" '
	$1 == "" && $2 != "" && $2 !~ /^(base|head)$/ { unit = $2 }
	unit == "sec/op" && $1 ~ /^Scan\// {
		name = $1
		sub(/^Scan\//, "", name)
		sub(/-[0-9]+$/, "", name)
		change = $6
		if (change == "~") change = "no change"
		else if (change ~ /^\+/ && change + 0 > max) change = change " :x:"
		printf "| %s | %.0f ms | %.0f ms | %s |\n", name, $2 * 1000, $4 * 1000, change
	}
' >"$work/rows.md"
slower=$(grep ':x:' "$work/rows.md" || true)
if [ -n "$slower" ]; then
	verdict=":x: A scan is more than $max_slowdown% slower than base."
else
	verdict=":white_check_mark: No scan is more than $max_slowdown% slower than base."
fi

# BENCH_REPORT receives the result as Markdown, for a pull request comment.
report=${BENCH_REPORT:-$work/report.md}
{
	printf '### Scan speed\n\n%s\n\n' "$verdict"
	printf '| Scan | base %s | this change | Change |\n|---|--:|--:|---|\n' "$(git rev-parse --short "$base")"
	cat "$work/rows.md"
	printf '\nMedian of %s runs each.\n\n' "$rounds"
	printf '<details><summary>benchstat</summary>\n\n```\n%s\n```\n\n</details>\n' "$(cat "$work/benchstat.txt")"
} >"$report"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	cat "$report" >>"$GITHUB_STEP_SUMMARY"
fi

if [ -n "$slower" ]; then
	echo "slower than $base by more than $max_slowdown%:"
	echo "$slower"
	exit 1
fi
