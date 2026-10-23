#!/bin/sh
# Times `lopper scan` built from a base commit and from this checkout on the
# same generated repositories. It fails when a scan starts more git processes,
# or is significantly slower by more than BENCH_MAX_SLOWDOWN percent. The two
# builds take turns, so a noisy runner slows both alike. BENCH_OUT receives
# report.md, for a pull request comment, and the raw base.txt and head.txt,
# for benchstat.
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
out=${BENCH_OUT:-$work}
mkdir -p "$out"
rm -f "$out/base.txt" "$out/head.txt" "$out/report.md"

mkdir "$work/base"
git -C "$root" archive "$base" apps/lopper | tar -x -C "$work/base"
(cd "$work/base/apps/lopper" && go build -o "$work/lopper-base" ./cmd/lopper)
go build -o "$work/lopper-head" ./cmd/lopper
go test -c -o "$work/bench.test" ./cmd/lopper

export LOPPER_BENCH_FIXTURE="$work/fixture"
bench() {
	if ! LOPPER_BENCH_BIN="$work/lopper-$1" "$work/bench.test" -test.run '^$' -test.bench . -test.benchtime 1x >"$work/run.txt" 2>&1; then
		cat "$work/run.txt" >&2
		return 1
	fi
	cat "$work/run.txt"
}
# The first scans fill the file system caches and are not counted.
bench base >/dev/null
bench head >/dev/null
i=0
while [ "$i" -lt "$rounds" ]; do
	bench base >>"$out/base.txt"
	bench head >>"$out/head.txt"
	i=$((i + 1))
done

benchstat() {
	go tool -modfile tools/go.mod benchstat "$@" base="$out/base.txt" head="$out/head.txt"
}
benchstat >"$work/benchstat.txt"
cat "$work/benchstat.txt"
benchstat -format csv >"$work/benchstat.csv" 2>"$work/benchstat.err" || {
	cat "$work/benchstat.err" >&2
	exit 1
}

# benchstat prints a time change only when it is significant, and "~"
# otherwise.
awk -F, -v max="$max_slowdown" '
	function number(s) { return s ~ /^[0-9.e+-]+$/ }
	$1 == "" && $2 != "" && $2 !~ /^(base|head)$/ { unit = $2 }
	$1 !~ /^Scan\// { next }
	{
		name = $1
		sub(/^Scan\//, "", name)
		sub(/-[0-9]+$/, "", name)
		if (!(name in seen)) { seen[name] = 1; order[++n] = name }
		if (!number($2) || !number($4)) {
			print name " has no " unit " for base or head" >"/dev/stderr"
			bad = 1
		}
	}
	unit == "git/op" {
		change = "no change"
		if ($4 != $2) change = sprintf("%+d", $4 - $2)
		if ($4 > $2) change = change " :x:"
		git[name] = sprintf("| %s: git processes | %d | %d | %s |", name, $2, $4, change)
	}
	unit == "sec/op" {
		change = $6
		if (change == "~") change = "not significant"
		else if (change ~ /^\+/ && change + 0 > max) change = change " :x:"
		time[name] = sprintf("| %s: time | %.0f ms | %.0f ms | %s |", name, $2 * 1000, $4 * 1000, change)
	}
	END {
		for (i = 1; i <= n; i++) {
			if (!(order[i] in git) || !(order[i] in time)) {
				print order[i] " lacks git/op or sec/op" >"/dev/stderr"
				bad = 1
			}
			print git[order[i]]
			print time[order[i]]
		}
		if (n == 0) {
			print "benchstat compared no scans" >"/dev/stderr"
			bad = 1
		}
		exit bad
	}
' "$work/benchstat.csv" >"$work/rows.md"

failures=""
if grep -q 'git processes.*:x:' "$work/rows.md"; then
	failures="starts more git processes"
fi
if grep -q 'time.*:x:' "$work/rows.md"; then
	failures="${failures:+$failures and }scans significantly more than $max_slowdown% slower"
fi
if [ -n "$failures" ]; then
	verdict=":x: This change $failures than base."
else
	verdict=":white_check_mark: No more git processes, and no significant slowdown over $max_slowdown%."
fi

{
	printf '### Scan speed\n\n%s\n\n' "$verdict"
	printf '| | base %s | this change | Change |\n|---|--:|--:|---|\n' "$(git rev-parse --short "$base")"
	cat "$work/rows.md"
	printf '\nTime is the median of %s runs each.\n\n' "$rounds"
	printf '<details><summary>benchstat</summary>\n\n```\n%s\n```\n\n</details>\n' "$(cat "$work/benchstat.txt")"
} >"$out/report.md"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	cat "$out/report.md" >>"$GITHUB_STEP_SUMMARY"
fi

if [ -n "$failures" ]; then
	echo "this change $failures than $base:"
	cat "$work/rows.md"
	exit 1
fi
