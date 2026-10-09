#!/usr/bin/env bash
# Owner-run post-release submission. Uses existing gh authentication only.
set -euo pipefail

fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail 'Usage: scripts/winget-submit.sh vMAJOR.MINOR.PATCH'
tag="$1"
LC_ALL=C
[[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail 'Only stable release tags are accepted'
version="${tag#v}"
branch="tslink-${version}"
manifest_dir="manifests/a/anydoor7/TSLink/${version}"
script_dir="$(cd "$(dirname "$0")" && pwd)"
for command in gh git python3 cosign; do
    command -v "$command" >/dev/null || fail "Required command missing: ${command}"
done
scratch="$(mktemp -d "${TMPDIR:-/tmp}/tslink-winget-submit.XXXXXXXX")"
trap 'rm -rf "$scratch"' EXIT

# A real upstream file is the positive control for the version-directory 404.
# Fail closed on authentication, rate limits, transport errors, or other status.
gh api repos/microsoft/winget-pkgs/contents/doc/manifest/README.md --jq 'select(.type == "file") | .sha' > "$scratch/control"
[[ "$(cat "$scratch/control")" =~ ^[0-9a-f]{40}$ ]] || fail 'Upstream positive control returned no file SHA'
if gh api "repos/microsoft/winget-pkgs/contents/${manifest_dir}" > "$scratch/existing" 2> "$scratch/readback-error"; then
    fail "${manifest_dir} already exists upstream; do not submit again"
fi
if ! python3 - "$scratch/readback-error" <<'PY'
from pathlib import Path
import re
import sys
sys.exit(0 if re.search(r"\(HTTP 404\)", Path(sys.argv[1]).read_text()) else 1)
PY
then
    fail 'Upstream version lookup failed without HTTP 404; nothing was changed'
fi
# Inspect all open PR pages, including file paths, rather than trusting an empty
# search-index result or a truncated first page to rule out a duplicate.
gh api --paginate --slurp 'repos/microsoft/winget-pkgs/pulls?state=open&per_page=100' > "$scratch/pulls.json"
python3 - "$scratch/pulls.json" "$scratch/numbers" "$version" "$branch" <<'PY'
import json
from pathlib import Path
import sys
pages = json.loads(Path(sys.argv[1]).read_text())
numbers = []
for page in pages:
    for pr in page:
        title = pr["title"].lower()
        if ("anydoor7.tslink" in title and sys.argv[3] in title) or (
                pr["head"]["ref"] == sys.argv[4]
                and pr["head"]["repo"] and pr["head"]["repo"]["full_name"] == "anydoor7/winget-pkgs"):
            sys.exit("An open PR already covers this package/version: " + pr["html_url"])
        numbers.append(str(pr["number"]))
Path(sys.argv[2]).write_text("\n".join(numbers))
PY
while IFS= read -r number || [ -n "$number" ]; do
    gh api --paginate --slurp "repos/microsoft/winget-pkgs/pulls/${number}/files?per_page=100" > "$scratch/files.json"
    python3 - "$scratch/files.json" "$manifest_dir/" <<'PY'
import json
from pathlib import Path
import sys
if any(f["filename"].startswith(sys.argv[2]) for page in json.loads(Path(sys.argv[1]).read_text()) for f in page):
    sys.exit("An open PR already changes this manifest directory")
PY
done < "$scratch/numbers"

gh api repos/anydoor7/winget-pkgs > "$scratch/fork.json"
python3 - "$scratch/fork.json" <<'PY'
import json
from pathlib import Path
import sys
repo = json.loads(Path(sys.argv[1]).read_text())
if (not repo.get("fork") or repo.get("parent", {}).get("full_name") != "microsoft/winget-pkgs"
        or repo.get("private") or repo.get("archived") or repo.get("default_branch") != "master"):
    sys.exit("anydoor7/winget-pkgs must already be a public, unarchived fork with default branch master")
PY
# Verify the release before the first remote mutation. Submission has no
# --allow-unverified escape hatch.
python3 "$script_dir/winget-manifests.py" "$tag" --out "$scratch/generated"
gh repo sync anydoor7/winget-pkgs --source microsoft/winget-pkgs --branch master
# Sync failure (including workflow permission refusal) stops before branching.
# No force-sync or force-push; an existing branch also stops for owner inspection.
git -c credential.helper= -c 'credential.helper=!gh auth git-credential' \
    clone --depth 1 --single-branch --branch master https://github.com/anydoor7/winget-pkgs.git "$scratch/fork"
cd "$scratch/fork"
set +e
git ls-remote --exit-code --heads origin "$branch" > "$scratch/remote-branch"
status="$?"
set -e
[ "$status" -ne 0 ] || fail "Branch ${branch} already exists; inspect it instead of overwriting"
[ "$status" -eq 2 ] || fail 'Cannot confirm the submission branch is absent'
git checkout -b "$branch"
mkdir -p "$manifest_dir"
files=("${manifest_dir}/anydoor7.TSLink.installer.yaml" "${manifest_dir}/anydoor7.TSLink.locale.en-US.yaml" "${manifest_dir}/anydoor7.TSLink.yaml")
for file in "${files[@]}"; do cp "$scratch/generated/$file" "$file"; done
git add -- "${files[@]}"
git diff --cached --name-only > "$scratch/staged"
printf '%s\n' "${files[@]}" | sort > "$scratch/expected"
sort "$scratch/staged" > "$scratch/actual"
cmp "$scratch/expected" "$scratch/actual" || fail 'The commit must contain exactly the three manifest YAMLs'
git -c user.name=monody0007 -c user.email=52037177+monody0007@users.noreply.github.com \
    commit -m "New version: anydoor7.TSLink version ${version}"
git -c credential.helper= -c 'credential.helper=!gh auth git-credential' push --set-upstream origin "$branch"
cat > "$scratch/pr-body.md" <<EOF
Adds anydoor7.TSLink ${version} from the published ${tag} release.
The x64 and ARM64 hashes come from checksums.txt verified with cosign against
the TSLink release workflow identity. Contains only the three winget 1.12.0 YAMLs.

Windows native manifest validation, install and upgrade have not been run by
this script. Maintainers must record those checks before claiming acceptance.
EOF
gh pr create --repo microsoft/winget-pkgs --base master --head "anydoor7:${branch}" \
    --title "New version: anydoor7.TSLink version ${version}" --body-file "$scratch/pr-body.md"
