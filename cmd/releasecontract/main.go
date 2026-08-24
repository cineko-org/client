package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	releasepb "github.com/cineko-org/contracts/v3/gen/go/cineko/release"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const usage = `usage:
  releasecontract release COMPONENT VERSION PLATFORM/ARCH ARTIFACT EXECUTABLE PUBLIC_URL PUBLISHED_AT
  releasecontract set COMPONENT RELEASE_JSON...
  releasecontract component RELEASE_JSON...
  releasecontract verify-set COMPONENT SET_JSON
  releasecontract fingerprint COMPONENT SET_JSON
  releasecontract verify-release RELEASE_JSON...
  releasecontract verify-artifacts RELEASE_JSON...
  releasecontract runtime PLATFORM/ARCH CLIENT_SET BROWSER_SET PLAYWRIGHT_SET
  releasecontract plan COMPONENT PUBLIC_BASE_URL RELEASE_JSON...`

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(strings.TrimSpace(usage))
	}
	switch args[0] {
	case "release":
		return writeRelease(args[1:])
	case "set":
		return writeSet(args[1:])
	case "component":
		return writeComponent(args[1:])
	case "verify-set":
		return verifySet(args[1:])
	case "fingerprint":
		return writeSetFingerprint(args[1:])
	case "verify-release":
		return verifyReleaseFiles(args[1:])
	case "verify-artifacts":
		return verifyArtifacts(args[1:])
	case "runtime":
		return writeRuntime(args[1:])
	case "plan":
		return writePlan(args[1:])
	default:
		return errors.New(strings.TrimSpace(usage))
	}
}

func writeRuntime(args []string) error {
	if len(args) != 4 {
		return errors.New(strings.TrimSpace(usage))
	}
	platform, architecture, err := splitPlatform(args[0])
	if err != nil {
		return err
	}
	clients := &releasepb.ClientReleaseSet{}
	browsers := &releasepb.BrowserReleaseSet{}
	playwrights := &releasepb.PlaywrightReleaseSet{}
	for _, input := range []struct {
		path    string
		message proto.Message
	}{{args[1], clients}, {args[2], browsers}, {args[3], playwrights}} {
		if err := readValidated(input.path, input.message); err != nil {
			return err
		}
	}
	client := findRelease(clients.GetReleases(), platform, architecture)
	browser := findRelease(browsers.GetReleases(), platform, architecture)
	playwright := findRelease(playwrights.GetReleases(), platform, architecture)
	if client == nil || browser == nil || playwright == nil {
		return fmt.Errorf("release sets do not contain %s/%s", platform, architecture)
	}
	runtimeRelease := releasepb.RuntimeRelease_builder{
		Client: client, Browser: browser, Playwright: playwright,
	}.Build()
	return marshalValidated(os.Stdout, runtimeRelease)
}

func findRelease[T interface {
	proto.Message
	GetPlatform() string
	GetArchitecture() string
}](releases []T, platform string, architecture string) T {
	var zero T
	for _, release := range releases {
		if release.GetPlatform() == platform && release.GetArchitecture() == architecture {
			return release
		}
	}
	return zero
}

// writeSetFingerprint emits a semantic content address from the validated
// generated message, independent of ProtoJSON whitespace or object ordering.
func writeSetFingerprint(args []string) error {
	if len(args) != 2 {
		return errors.New(strings.TrimSpace(usage))
	}
	message, err := emptyReleaseSet(args[0])
	if err != nil {
		return err
	}
	if err := readValidated(args[1], message); err != nil {
		return err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal release set fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	_, err = fmt.Fprintln(os.Stdout, hex.EncodeToString(digest[:]))
	return err
}

func writeComponent(paths []string) error {
	component, err := releaseComponent(paths)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, component)
	return err
}

func writeRelease(args []string) error {
	if len(args) != 7 {
		return errors.New(strings.TrimSpace(usage))
	}
	component, version := args[0], strings.TrimPrefix(args[1], "v")
	platform, architecture, err := splitPlatform(args[2])
	if err != nil {
		return err
	}
	artifactPath, executable, publicURL := args[3], args[4], args[5]
	publishedAt, err := parseTimestamp(args[6])
	if err != nil {
		return err
	}
	artifact, err := releaseArtifact(artifactPath, publicURL, executable)
	if err != nil {
		return err
	}
	channel := "stable"
	var message proto.Message
	switch component {
	case "client":
		minimumLauncher, err := requiredEnv("CINEKO_MINIMUM_LAUNCHER_VERSION")
		if err != nil {
			return err
		}
		minimumBrowser, err := requiredEnv("CINEKO_BROWSER_REVISION")
		if err != nil {
			return err
		}
		playwrightVersion, err := requiredEnv("CINEKO_PLAYWRIGHT_VERSION")
		if err != nil {
			return err
		}
		minimumLauncher = strings.TrimPrefix(minimumLauncher, "v")
		playwrightVersion = strings.TrimPrefix(playwrightVersion, "v")
		message = releasepb.ClientRelease_builder{
			Channel: &channel, Platform: &platform, Architecture: &architecture, Version: &version,
			MinimumLauncherVersion: &minimumLauncher, MinimumBrowserRevision: &minimumBrowser,
			PlaywrightVersion: &playwrightVersion, Artifact: artifact, PublishedAt: publishedAt,
		}.Build()
	case "browser":
		playwrightVersions, err := requiredEnv("CINEKO_PLAYWRIGHT_VERSIONS")
		if err != nil {
			return err
		}
		compatibleVersions := strings.FieldsFunc(playwrightVersions, func(value rune) bool { return value == ',' })
		for index := range compatibleVersions {
			compatibleVersions[index] = strings.TrimPrefix(strings.TrimSpace(compatibleVersions[index]), "v")
		}
		message = releasepb.BrowserRelease_builder{
			Channel: &channel, Platform: &platform, Architecture: &architecture, Revision: &version,
			CompatiblePlaywrightVersions: compatibleVersions, Artifact: artifact, PublishedAt: publishedAt,
		}.Build()
	case "playwright":
		message = releasepb.PlaywrightRelease_builder{
			Channel: &channel, Platform: &platform, Architecture: &architecture, Version: &version,
			Artifact: artifact, PublishedAt: publishedAt,
		}.Build()
	default:
		return fmt.Errorf("unsupported release component %q", component)
	}
	return marshalValidated(os.Stdout, message)
}

func writeSet(args []string) error {
	if len(args) < 2 {
		return errors.New(strings.TrimSpace(usage))
	}
	message, err := readReleaseSet(args[0], args[1:])
	if err != nil {
		return err
	}
	return marshalValidated(os.Stdout, message)
}

func verifySet(args []string) error {
	if len(args) != 2 {
		return errors.New(strings.TrimSpace(usage))
	}
	message, err := emptyReleaseSet(args[0])
	if err != nil {
		return err
	}
	return readValidated(args[1], message)
}

func verifyArtifacts(paths []string) error {
	if err := verifyReleaseFiles(paths); err != nil {
		return err
	}
	component, err := releaseComponent(paths)
	if err != nil {
		return err
	}
	if len(paths) != 3 {
		return errors.New("release metadata must contain exactly three platforms")
	}
	_, err = readReleaseSet(component, paths)
	return err
}

func verifyReleaseFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New(strings.TrimSpace(usage))
	}
	for _, path := range paths {
		_, message, err := readRelease(path)
		if err != nil {
			return err
		}
		if err := verifyArtifactFile(path, releaseArtifactValue(message)); err != nil {
			return err
		}
	}
	return nil
}

func releaseComponent(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New(strings.TrimSpace(usage))
	}
	component := ""
	for _, path := range paths {
		current, _, err := readRelease(path)
		if err != nil {
			return "", err
		}
		if component == "" {
			component = current
		} else if current != component {
			return "", errors.New("release metadata mixes components")
		}
	}
	return component, nil
}

func writePlan(args []string) error {
	if len(args) < 3 {
		return errors.New(strings.TrimSpace(usage))
	}
	component, publicBase, paths := args[0], strings.TrimSuffix(args[1], "/"), args[2:]
	if _, err := url.ParseRequestURI(publicBase); err != nil || !strings.HasPrefix(publicBase, "https://") {
		return errors.New("public release base must be an HTTPS URL")
	}
	if err := verifyArtifacts(paths); err != nil {
		return err
	}
	for _, path := range paths {
		current, message, err := readRelease(path)
		if err != nil {
			return err
		}
		if current != component {
			return fmt.Errorf("release metadata component %q does not match %q", current, component)
		}
		artifact := releaseArtifactValue(message)
		prefix := publicBase + "/"
		if artifact == nil || !strings.HasPrefix(artifact.GetUrl(), prefix) {
			return fmt.Errorf("release URL is outside public base: %s", path)
		}
		artifactPath := filepath.Join(filepath.Dir(path), filepath.Base(artifact.GetUrl()))
		fmt.Printf("%s\t%s\t%s\t%d\t%s\n", artifactPath, strings.TrimPrefix(artifact.GetUrl(), prefix), artifact.GetUrl(), artifact.GetSize(), artifact.GetSha256())
	}
	return nil
}

func readReleaseSet(component string, paths []string) (proto.Message, error) {
	switch component {
	case "client":
		values, err := readReleaseValues(paths, func() *releasepb.ClientRelease {
			return &releasepb.ClientRelease{}
		})
		if err != nil {
			return nil, err
		}
		message := releasepb.ClientReleaseSet_builder{Releases: values}.Build()
		return message, protovalidate.Validate(message)
	case "browser":
		values, err := readReleaseValues(paths, func() *releasepb.BrowserRelease {
			return &releasepb.BrowserRelease{}
		})
		if err != nil {
			return nil, err
		}
		message := releasepb.BrowserReleaseSet_builder{Releases: values}.Build()
		return message, protovalidate.Validate(message)
	case "playwright":
		values, err := readReleaseValues(paths, func() *releasepb.PlaywrightRelease {
			return &releasepb.PlaywrightRelease{}
		})
		if err != nil {
			return nil, err
		}
		message := releasepb.PlaywrightReleaseSet_builder{Releases: values}.Build()
		return message, protovalidate.Validate(message)
	default:
		return nil, fmt.Errorf("unsupported release component %q", component)
	}
}

func readReleaseValues[T interface {
	proto.Message
	GetPlatform() string
	GetArchitecture() string
}](paths []string, newValue func() T) ([]T, error) {
	values := make([]T, 0, len(paths))
	for _, path := range paths {
		value := newValue()
		if err := readValidated(path, value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return releaseKey(values[i]) < releaseKey(values[j]) })
	return values, nil
}

func emptyReleaseSet(component string) (proto.Message, error) {
	switch component {
	case "client":
		return &releasepb.ClientReleaseSet{}, nil
	case "browser":
		return &releasepb.BrowserReleaseSet{}, nil
	case "playwright":
		return &releasepb.PlaywrightReleaseSet{}, nil
	default:
		return nil, fmt.Errorf("unsupported release component %q", component)
	}
}

func readRelease(path string) (string, proto.Message, error) {
	for _, candidate := range []struct {
		component string
		message   proto.Message
	}{
		{"client", &releasepb.ClientRelease{}},
		{"browser", &releasepb.BrowserRelease{}},
		{"playwright", &releasepb.PlaywrightRelease{}},
	} {
		if err := readValidated(path, candidate.message); err == nil && releaseArtifactValue(candidate.message) != nil {
			return candidate.component, candidate.message, nil
		}
	}
	return "", nil, fmt.Errorf("%s is not a supported generated release message", path)
}

func readValidated(path string, message proto.Message) error {
	payload, err := readOperatorFile(path)
	if err != nil {
		return err
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, message); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := protovalidate.Validate(message); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	if err := validateReleaseContract(message); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	return nil
}

func marshalValidated(destination *os.File, message proto.Message) error {
	if err := protovalidate.Validate(message); err != nil {
		return fmt.Errorf("validate generated release message: %w", err)
	}
	if err := validateReleaseContract(message); err != nil {
		return fmt.Errorf("validate generated release message: %w", err)
	}
	payload, err := (protojson.MarshalOptions{UseProtoNames: false, Indent: "  "}).Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(destination, "%s\n", payload)
	return err
}

func validateReleaseContract(message proto.Message) error {
	switch value := message.(type) {
	case *releasepb.ClientRelease:
		return validateClientRelease(value)
	case *releasepb.BrowserRelease:
		return validateBrowserRelease(value)
	case *releasepb.PlaywrightRelease:
		return validatePlaywrightRelease(value)
	case *releasepb.ClientReleaseSet:
		return validateReleaseSet(value.GetReleases())
	case *releasepb.BrowserReleaseSet:
		return validateReleaseSet(value.GetReleases())
	case *releasepb.PlaywrightReleaseSet:
		return validateReleaseSet(value.GetReleases())
	case *releasepb.RuntimeRelease:
		return validateRuntimeRelease(value)
	default:
		return nil
	}
}

func validateRuntimeRelease(value *releasepb.RuntimeRelease) error {
	if value == nil || value.GetClient() == nil || value.GetBrowser() == nil || value.GetPlaywright() == nil {
		return errors.New("runtime release components are required")
	}
	client, browser, playwright := value.GetClient(), value.GetBrowser(), value.GetPlaywright()
	if err := validateClientRelease(client); err != nil {
		return err
	}
	if err := validateBrowserRelease(browser); err != nil {
		return err
	}
	if err := validatePlaywrightRelease(playwright); err != nil {
		return err
	}
	if releaseKey(client) != releaseKey(browser) || releaseKey(client) != releaseKey(playwright) {
		return errors.New("runtime release platforms do not match")
	}
	if client.GetChannel() != browser.GetChannel() || client.GetChannel() != playwright.GetChannel() {
		return errors.New("runtime release channels do not match")
	}
	if client.GetMinimumBrowserRevision() != browser.GetRevision() ||
		client.GetPlaywrightVersion() != playwright.GetVersion() ||
		!contains(browser.GetCompatiblePlaywrightVersions(), playwright.GetVersion()) {
		return errors.New("runtime release compatibility does not match")
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateClientRelease(value *releasepb.ClientRelease) error {
	if value.GetChannel() == "" || value.GetMinimumLauncherVersion() == "" || value.GetMinimumBrowserRevision() == "" || value.GetPlaywrightVersion() == "" {
		return errors.New("client release compatibility fields are required")
	}
	return validateReleaseFields(value.GetArtifact(), value.GetPublishedAt(), value.GetVersion())
}

func validateBrowserRelease(value *releasepb.BrowserRelease) error {
	if value.GetChannel() == "" || len(value.GetCompatiblePlaywrightVersions()) == 0 {
		return errors.New("browser release compatibility fields are required")
	}
	return validateReleaseFields(value.GetArtifact(), value.GetPublishedAt(), value.GetRevision())
}

func validatePlaywrightRelease(value *releasepb.PlaywrightRelease) error {
	if value.GetChannel() == "" {
		return errors.New("playwright release channel is required")
	}
	return validateReleaseFields(value.GetArtifact(), value.GetPublishedAt(), value.GetVersion())
}

func validateReleaseFields(artifact *releasepb.Artifact, publishedAt *timestamppb.Timestamp, identity string) error {
	if identity == "" {
		return errors.New("release identity is required")
	}
	if artifact == nil || artifact.GetSize() <= 0 || artifact.GetExecutable() == "" || !sha256Pattern.MatchString(artifact.GetSha256()) {
		return errors.New("release artifact is incomplete")
	}
	parsedURL, err := url.Parse(artifact.GetUrl())
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return errors.New("release artifact URL must be HTTPS")
	}
	if publishedAt == nil || publishedAt.CheckValid() != nil {
		return errors.New("release publication timestamp is required")
	}
	return nil
}

func validateReleaseSet[T interface {
	proto.Message
	GetPlatform() string
	GetArchitecture() string
	GetPublishedAt() *timestamppb.Timestamp
}](releases []T) error {
	if len(releases) != 3 {
		return errors.New("release set must contain exactly three platforms")
	}
	want := map[string]bool{"darwin/arm64": true, "linux/amd64": true, "windows/amd64": true}
	seen := make(map[string]bool, len(releases))
	var publishedAt time.Time
	for index, release := range releases {
		if err := validateReleaseContract(release); err != nil {
			return err
		}
		key := releaseKey(release)
		if !want[key] || seen[key] {
			return fmt.Errorf("invalid or duplicate release platform %q", key)
		}
		seen[key] = true
		current := release.GetPublishedAt().AsTime()
		if index == 0 {
			publishedAt = current
		} else if !current.Equal(publishedAt) {
			return errors.New("release set publication timestamps must match")
		}
	}
	return nil
}

func releaseArtifact(path, publicURL, executable string) (*releasepb.Artifact, error) {
	info, err := statOperatorFile(path)
	if err != nil {
		return nil, err
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	size := info.Size()
	return releasepb.Artifact_builder{Url: &publicURL, Size: &size, Sha256: &hash, Executable: &executable}.Build(), nil
}

func verifyArtifactFile(metadataPath string, artifact *releasepb.Artifact) error {
	if artifact == nil {
		return fmt.Errorf("release metadata has no artifact: %s", metadataPath)
	}
	path := filepath.Join(filepath.Dir(metadataPath), filepath.Base(artifact.GetUrl()))
	info, err := statOperatorFile(path)
	if err != nil {
		return err
	}
	if info.Size() != artifact.GetSize() {
		return fmt.Errorf("artifact size mismatch: %s", path)
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if hash != artifact.GetSha256() {
		return fmt.Errorf("artifact checksum mismatch: %s", path)
	}
	return nil
}

func releaseArtifactValue(message proto.Message) *releasepb.Artifact {
	switch value := message.(type) {
	case *releasepb.ClientRelease:
		return value.GetArtifact()
	case *releasepb.BrowserRelease:
		return value.GetArtifact()
	case *releasepb.PlaywrightRelease:
		return value.GetArtifact()
	default:
		return nil
	}
}

func releaseKey(value interface {
	GetPlatform() string
	GetArchitecture() string
}) string {
	return value.GetPlatform() + "/" + value.GetArchitecture()
}

func fileSHA256(path string) (string, error) {
	payload, err := readOperatorFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func readOperatorFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is required")
	}
	// #nosec G304,G703 -- this local operator CLI intentionally accepts explicit file paths.
	return os.ReadFile(filepath.Clean(path))
}

func statOperatorFile(path string) (os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("file path is required")
	}
	// #nosec G703 -- this local operator CLI intentionally accepts explicit file paths.
	return os.Stat(filepath.Clean(path))
}

func splitPlatform(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid platform %q", value)
	}
	return parts[0], parts[1], nil
}

func parseTimestamp(value string) (*timestamppb.Timestamp, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("invalid release timestamp: %w", err)
	}
	timestamp := timestamppb.New(parsed)
	return timestamp, timestamp.CheckValid()
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New(name + " is required")
	}
	return value, nil
}
