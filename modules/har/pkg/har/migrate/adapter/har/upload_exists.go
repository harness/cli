package har

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	http2 "net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// uploadExists checks the native package endpoint that serves what the matching
// upload*File call writes.
func (c *client) uploadExists(
	ctx context.Context,
	registry string,
	f *types.File,
	artifactName, version string,
	artifactType types.ArtifactType,
	metadata map[string]interface{},
) (bool, error) {
	base := fmt.Sprintf("%s/pkg/%s/%s/", strings.TrimRight(c.url, "/"), c.accountID, path.Base(registry))
	switch artifactType {
	case types.CONDA:
		subdir, _ := metadata["X-Subdir"].(string)
		fileName, _ := metadata["X-File-Name"].(string)
		return c.existsAt(ctx, http2.MethodHead, base, "conda/"+subdir+"/"+fileName, nil)
	case types.SWIFT:
		scope, name, ok := strings.Cut(artifactName, ".")
		if !ok {
			return false, fmt.Errorf("invalid Swift package name format: %s, expected format: scope.name", artifactName)
		}
		header := http2.Header{"Accept": []string{"application/vnd.swift.registry.v1+json"}}
		return c.existsAt(ctx, http2.MethodGet, base, "swift/"+scope+"/"+name+"/"+version, header)
	case types.COMPOSER:
		vendor, name, ok := strings.Cut(artifactName, "/")
		if !ok {
			return false, fmt.Errorf("invalid Composer package name %q, expected vendor/package", artifactName)
		}
		rel := fmt.Sprintf("composer/dist/%s/%s/%s/%s-%s.zip", vendor, name, version, name, version)
		return c.existsAt(ctx, http2.MethodGet, base, rel, nil)
	case types.RPM:
		fileName := path.Base(f.Uri)
		name, versionRelease, arch, ok := parseRPMFileName(fileName)
		if !ok {
			return false, fmt.Errorf("cannot parse RPM file name %q", fileName)
		}
		exists, err := c.existsAt(ctx, http2.MethodGet, base,
			fmt.Sprintf("rpm/package/%s/%s/%s/%s", name, versionRelease, arch, fileName), nil)
		if err != nil || exists {
			return exists, err
		}
		// HAR's version and download path include the epoch, which the file name lacks.
		files, err := c.rpmRepodataFiles(ctx, base)
		if err != nil {
			return false, err
		}
		return files[fileName], nil
	case types.CONAN:
		return c.existsAt(ctx, http2.MethodGet, base, conanFilePath(metadata), nil)
	case types.DEBIAN:
		return c.debianFileExists(ctx, base, path.Base(f.Uri), metadata)
	default:
		return false, fmt.Errorf("upload existence check not supported for %s", artifactType)
	}
}

// rubyGemExists HEADs the gem download path; HAR's version file listing is empty for gems.
func (c *client) rubyGemExists(ctx context.Context, registryRef string, f *types.File) (bool, error) {
	base := fmt.Sprintf("%s/pkg/%s/%s/", strings.TrimRight(c.url, "/"), c.accountID, path.Base(registryRef))
	return c.existsAt(ctx, http2.MethodHead, base, "ruby/gems/"+path.Base(f.Uri), nil)
}

// parseRPMFileName splits "<name>-<version>-<release>.<arch>.rpm"; names may contain "-".
func parseRPMFileName(fileName string) (name, versionRelease, arch string, ok bool) {
	stem, found := strings.CutSuffix(fileName, ".rpm")
	if !found {
		return "", "", "", false
	}
	dot := strings.LastIndex(stem, ".")
	if dot < 0 {
		return "", "", "", false
	}
	stem, arch = stem[:dot], stem[dot+1:]
	releaseDash := strings.LastIndex(stem, "-")
	if releaseDash < 0 {
		return "", "", "", false
	}
	versionDash := strings.LastIndex(stem[:releaseDash], "-")
	if versionDash <= 0 {
		return "", "", "", false
	}
	return stem[:versionDash], stem[versionDash+1:], arch, arch != ""
}

func conanFilePath(metadata map[string]interface{}) string {
	get := func(key string) string {
		v, _ := metadata[key].(string)
		return v
	}
	user, channel := get("user"), get("channel")
	if user == "" {
		user = "_"
	}
	if channel == "" {
		channel = "_"
	}
	rel := fmt.Sprintf("conan/v2/conans/%s/%s/%s/%s/revisions/%s", get("name"), get("version"), user, channel,
		get("rrev"))
	if get("layer") == "package" {
		rel += fmt.Sprintf("/packages/%s/revisions/%s", get("pkgid"), get("prev"))
	}
	return rel + "/files/" + get("filename")
}

// debianFileExists looks the file up in its distribution's index rather than in the
// pool, which HAR shares across distributions: a pool hit does not mean the file is
// published in this distribution. A source file is matched within its own .dsc's
// Sources entry: an .orig tarball shared by several revisions is attached to each
// revision's .dsc separately.
func (c *client) debianFileExists(
	ctx context.Context,
	base, fileName string,
	metadata map[string]interface{},
) (bool, error) {
	distribution, _ := metadata["distribution"].(string)
	component, _ := metadata["component"].(string)
	fileType, _ := metadata["fileType"].(string)
	var rel string
	key := fileName
	switch fileType {
	case "src":
		packageName, _ := metadata["packageName"].(string)
		sourceVersion, _ := metadata["sourceVersion"].(string)
		if packageName == "" || sourceVersion == "" {
			return false, fmt.Errorf("missing package name or version for debian source file %q", fileName)
		}
		key = debianSourceKey(packageName, sourceVersion, fileName)
		rel = fmt.Sprintf("debian/dists/%s/%s/source/Sources", distribution, component)
	case "dsc":
		rel = fmt.Sprintf("debian/dists/%s/%s/source/Sources", distribution, component)
	case "", "deb":
		stem := strings.TrimSuffix(fileName, ".deb")
		arch := stem[strings.LastIndex(stem, "_")+1:]
		rel = fmt.Sprintf("debian/dists/%s/%s/binary-%s/Packages", distribution, component, arch)
	default:
		return false, fmt.Errorf("debian existence check not supported for file type %q", fileType)
	}
	files, err := c.debianIndexFiles(ctx, base, rel)
	if err != nil {
		return false, err
	}
	return files[key], nil
}

func debianSourceKey(packageName, version, fileName string) string {
	return packageName + "\x00" + version + "\x00" + fileName
}

type packageIndex struct {
	mu    sync.Mutex
	files map[string]bool
}

// cachedIndex loads a destination index once per run. Failed loads are not cached
// so a retry reloads.
func (c *client) cachedIndex(key string, load func() (map[string]bool, error)) (map[string]bool, error) {
	c.indexesMu.Lock()
	if c.indexes == nil {
		c.indexes = map[string]*packageIndex{}
	}
	idx := c.indexes[key]
	if idx == nil {
		idx = &packageIndex{}
		c.indexes[key] = idx
	}
	c.indexesMu.Unlock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.files != nil {
		return idx.files, nil
	}
	files, err := load()
	if err != nil {
		return nil, err
	}
	idx.files = files
	return files, nil
}

// debianIndexFiles returns the file names listed in a Packages or Sources index.
func (c *client) debianIndexFiles(ctx context.Context, base, rel string) (map[string]bool, error) {
	return c.cachedIndex(base+rel, func() (map[string]bool, error) {
		body, found, err := c.fetchWithoutLeakingAuth(ctx, base, rel)
		if err != nil {
			return nil, err
		}
		files := map[string]bool{}
		if found {
			defer body.Close()
			if err := parseDebianIndex(body, files); err != nil {
				return nil, fmt.Errorf("failed to read '%s': %w", rel, err)
			}
		}
		return files, nil
	})
}

// rpmRepodataFiles returns "<name>-<version>-<release>.<arch>.rpm" for every package
// in the registry's primary repodata, whatever its epoch.
func (c *client) rpmRepodataFiles(ctx context.Context, base string) (map[string]bool, error) {
	return c.cachedIndex(base+"rpm/repodata", func() (map[string]bool, error) {
		primary, err := c.rpmPrimaryLocation(ctx, base)
		if err != nil || primary == "" {
			return map[string]bool{}, err
		}
		body, found, err := c.fetchWithoutLeakingAuth(ctx, base, "rpm/"+primary)
		if err != nil {
			return nil, err
		}
		files := map[string]bool{}
		if found {
			defer body.Close()
			if err := parseRPMPrimary(body, files); err != nil {
				return nil, fmt.Errorf("failed to read '%s': %w", primary, err)
			}
		}
		return files, nil
	})
}

func (c *client) rpmPrimaryLocation(ctx context.Context, base string) (string, error) {
	body, found, err := c.fetchWithoutLeakingAuth(ctx, base, "rpm/repodata/repomd.xml")
	if err != nil || !found {
		return "", err
	}
	defer body.Close()
	var repomd struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
		} `xml:"data"`
	}
	if err := xml.NewDecoder(body).Decode(&repomd); err != nil {
		return "", fmt.Errorf("failed to read repomd.xml: %w", err)
	}
	for _, d := range repomd.Data {
		if d.Type == "primary" {
			return d.Location.Href, nil
		}
	}
	return "", nil
}

// parseRPMPrimary accepts gzip or plain XML: HAR serves primary.xml.gz uncompressed.
func parseRPMPrimary(r io.Reader, files map[string]bool) error {
	br := bufio.NewReader(r)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	} else {
		r = br
	}
	decoder := xml.NewDecoder(r)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "package" {
			continue
		}
		var pkg struct {
			Name    string `xml:"name"`
			Arch    string `xml:"arch"`
			Version struct {
				Ver string `xml:"ver,attr"`
				Rel string `xml:"rel,attr"`
			} `xml:"version"`
		}
		if err := decoder.DecodeElement(&pkg, &start); err != nil {
			return err
		}
		files[fmt.Sprintf("%s-%s-%s.%s.rpm", pkg.Name, pkg.Version.Ver, pkg.Version.Rel, pkg.Arch)] = true
	}
}

// parseDebianIndex collects file names from "Filename:" fields (Packages) and from
// the Files/Checksums sections (Sources). Sources files are also recorded under
// debianSourceKey for their entry's package and version.
func parseDebianIndex(r io.Reader, files map[string]bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var packageName, version string
	var entryFiles []string
	flush := func() {
		for _, name := range entryFiles {
			files[name] = true
			files[debianSourceKey(packageName, version, name)] = true
		}
		packageName, version, entryFiles = "", "", nil
	}
	inFileList := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			inFileList = false
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimSpace(value)
			inFileList = field == "Files" || strings.HasPrefix(field, "Checksums-")
			switch field {
			case "Filename":
				files[path.Base(value)] = true
			case "Package":
				packageName = value
			case "Version":
				version = value
			}
			continue
		}
		if fields := strings.Fields(line); inFileList && len(fields) >= 3 {
			entryFiles = append(entryFiles, fields[2])
		}
	}
	flush()
	return scanner.Err()
}

// fetchWithoutLeakingAuth GETs base+rel. A redirect to another host (signed blob URL)
// is followed without credentials, since c.client attaches the API key to every request.
func (c *client) fetchWithoutLeakingAuth(ctx context.Context, base, rel string) (io.ReadCloser, bool, error) {
	resp, err := c.doWithoutRedirect(ctx, http2.MethodGet, base, rel, nil)
	if err != nil {
		return nil, false, err
	}
	switch {
	case resp.StatusCode == http2.StatusNotFound:
		resp.Body.Close()
		return nil, false, nil
	case resp.StatusCode == http2.StatusOK:
		return resp.Body, true, nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		resp.Body.Close()
		location, err := resp.Location()
		if err != nil {
			return nil, false, fmt.Errorf("redirect without location for '%s': %w", rel, err)
		}
		httpClient := &http2.Client{Timeout: 5 * time.Minute}
		if location.Host == resp.Request.URL.Host {
			httpClient = c.client
		}
		req, err := http2.NewRequestWithContext(ctx, http2.MethodGet, location.String(), nil)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create request for '%s': %w", rel, err)
		}
		blobResp, err := httpClient.Do(req)
		if err != nil {
			return nil, false, fmt.Errorf("failed to download '%s': %w", rel, unwrapURLError(err))
		}
		if blobResp.StatusCode != http2.StatusOK {
			blobResp.Body.Close()
			return nil, false, types.NewStatusError(blobResp.StatusCode,
				"unexpected status code %d downloading '%s'", blobResp.StatusCode, rel)
		}
		return blobResp.Body, true, nil
	default:
		resp.Body.Close()
		return nil, false, types.NewStatusError(resp.StatusCode, "unexpected status code %d checking '%s'",
			resp.StatusCode, rel)
	}
}
