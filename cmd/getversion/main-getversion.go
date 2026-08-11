package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

var versionCore = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

func main() {
	var next bool
	var prefix string
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "--next":
			next = true
		case strings.HasPrefix(arg, "--prefix="):
			prefix = strings.TrimPrefix(arg, "--prefix=")
		}
	}

	out, err := exec.Command("git", "tag").Output()
	if err != nil {
		fmt.Println("0.1.0")
		return
	}

	var versions []string
	for _, tag := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		tag = strings.TrimSpace(tag)
		if prefix != "" {
			if !strings.HasPrefix(tag, prefix) {
				continue
			}
			tag = strings.TrimPrefix(tag, prefix)
		} else if strings.Contains(tag, "/") {
			// skip prefixed tags (e.g. har/v0.9.0) when scanning for unprefixed ones
			continue
		}
		if semver.IsValid(tag) {
			versions = append(versions, tag)
		}
	}

	if len(versions) == 0 {
		fmt.Println("0.1.0")
		return
	}

	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) < 0
	})

	latest := strings.TrimPrefix(versions[len(versions)-1], "v")

	if next {
		latest = bumpPatch(latest)
	}

	fmt.Println(latest)
}

// bumpPatch increments the patch version. If the version has a pre-release or
// build suffix (e.g. 0.1.7-beta.1), it strips the suffix instead — we're
// already working toward that release.
func bumpPatch(version string) string {
	m := versionCore.FindStringSubmatch(version)
	if m == nil {
		return version
	}
	// if version is exactly MAJOR.MINOR.PATCH, bump patch
	if m[0] == version {
		p, _ := strconv.Atoi(m[3])
		return m[1] + "." + m[2] + "." + strconv.Itoa(p+1)
	}
	// has pre-release or build metadata — already targeting this release
	return m[1] + "." + m[2] + "." + m[3]
}
