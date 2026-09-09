package cmd

// developmentVersionUnmeasured mirrors main's developmentVersion constant
// ("dev"): main assigns exactly this literal to Version, with an empty
// Commit, only when neither the release ldflags nor `go build`/`go install`'s
// embedded VCS metadata gave it anything to resolve (see
// resolveBuildVersion in main.go and its "missing VCS metadata"/"missing
// build info" test cases). selfBuildIdentity uses it to recognize that one
// specific "nothing was measurable" outcome, not to duplicate main's
// resolution — main.go already tried both evidence sources before Version
// and Commit were ever set.
const developmentVersionUnmeasured = "dev"

// selfBuildIdentity reports this process's own build identity for
// daemon/CLI build-skew comparisons ('tslink doctor'). Version and Commit are
// resolved once, in main.go, before Execute() runs: from the release
// -ldflags when a build set them, otherwise from the module version and VCS
// revision `go build`/`go install` embedded. A daemon started via `tslink
// serve` and a later `tslink doctor`/`tslink status` invocation both reach
// this function through that same resolution, however different their
// underlying binaries are, so there is exactly one implementation of "what is
// my build identity" for both sides to disagree by.
//
// selfBuildIdentity returns "" only when main.go's resolution found neither
// evidence source (Version is the bare "dev" fallback and Commit is empty).
// Callers must treat "" as "could not be determined" and skip the
// comparison, never as a build identity equal to another empty result — see
// the doctor build-skew finding, which suppresses its report when both sides
// return "".
func selfBuildIdentity() string {
	if Version == developmentVersionUnmeasured && Commit == "" {
		return ""
	}
	return versionDisplayString(Version, Commit)
}

// versionDisplayString applies TSLink's one release-identity format, so
// Execute()'s `tslink --version` rendering and selfBuildIdentity()'s
// daemon/CLI comparisons can never drift into two different renderings of
// the same (version, commit) pair.
func versionDisplayString(version, commit string) string {
	if commit == "" {
		return version
	}
	return version + " (" + commit + ")"
}
