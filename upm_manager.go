package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	templateToken        = "UPM-Template"
	maxRecentEditPaths   = 10
	packageJSONVersionRe = `"version"\s*:\s*"([^"]*)"`
)

var (
	packageVersionPattern = regexp.MustCompile(packageJSONVersionRe)
	packageNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
)

type validationSeverity int

const (
	severityError validationSeverity = iota
	severityWarn
)

type validationFinding struct {
	severity validationSeverity
	message  string
}

type packageManifestForValidate struct {
	Name    string `json:"name"`
	Samples []struct {
		Path string `json:"path"`
	} `json:"samples"`
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	defaultTemplatePath := resolveTemplatePath()

	fmt.Println("UPM Template Creator")
	if defaultTemplatePath == "" {
		fmt.Println("Were not able to detect template path")
	} else {
		fmt.Printf("Detected template path: %s\n", defaultTemplatePath)
	}
	fmt.Println()

	for {
		fmt.Println("Select an option:")
		fmt.Println("1) Create a new package")
		fmt.Println("2) Edit an existing package")
		fmt.Println("3) Validate a package layout")
		fmt.Println("4) Exit")
		choice := promptNonEmpty(reader, "Please enter your choice (1-4): ")
		fmt.Println()

		switch strings.TrimSpace(choice) {
		case "1":
			createTemplate(reader, defaultTemplatePath)
		case "2":
			editTemplate(reader)
		case "3":
			runValidatePackage(reader)
		case "4":
			fmt.Println("Exiting..")
			return
		default:
			fmt.Println("Invalid option. Enter 1, 2, 3, or 4.")
			fmt.Println()
		}
	}
}

func createTemplate(reader *bufio.Reader, sourceTemplatePath string) {
	resolvedSource := sourceTemplatePath
	if !dirExists(resolvedSource) {
		fmt.Println("Template folder was not auto-detected.")
		fmt.Println("Enter full path to your base UPM-Template folder:")
		resolvedSource = promptNonEmpty(reader, "Template path: ")
		fmt.Println()
	}

	if !dirExists(resolvedSource) {
		fmt.Printf("Template folder not found: %s\n", resolvedSource)
		fmt.Println("Make sure the path points to an existing UPM-Template folder.")
		fmt.Println()
		return
	}

	suggestedDestination := getSuggestedDestinationFolder(resolvedSource)
	fmt.Printf("Using template folder: %s\n\n", resolvedSource)

	pluginName := promptNonEmpty(reader, "Enter new package name: ")
	outputParent := promptAllowEmpty(
		reader,
		fmt.Sprintf("Enter destination folder (the app will create a sub-folder named as the new template there, with name '%s') (default: %s): ", pluginName, suggestedDestination),
		suggestedDestination,
	)

	if !dirExists(outputParent) {
		fmt.Println("Destination folder does not exist.")
		fmt.Println()
		return
	}

	targetPath := filepath.Join(outputParent, pluginName)
	if dirExists(targetPath) {
		fmt.Printf("Destination already exists: %s\n\n", targetPath)
		return
	}

	authorName := promptNonEmpty(reader, "Enter author name (replaces 'Author' in package.json and LICENSE): ")

	if err := copyDirectory(resolvedSource, targetPath); err != nil {
		fmt.Printf("Failed to copy template: %v\n\n", err)
		return
	}
	if err := applyTemplateTokenReplacements(targetPath, templateToken, pluginName, authorName, ""); err != nil {
		fmt.Printf("Failed to apply replacements: %v\n\n", err)
		return
	}

	includeRoadmap := promptYesNo(reader, "Include 'Roadmap.md'? (y/n) [y]: ", true)
	includeSamples := promptYesNo(reader, "Include 'Samples~' folder? (y/n) [y]: ", true)
	includeScreenshots := promptYesNo(reader, "Create 'Screenshots~' folder? (y/n) [y]: ", true)

	if !includeRoadmap {
		tryDeleteIfExists(filepath.Join(targetPath, "Roadmap.md"))
		tryDeleteIfExists(filepath.Join(targetPath, "Roadmap.md.meta"))
	}

	if !includeSamples {
		tryDeleteIfExists(filepath.Join(targetPath, "Samples~"))
		tryDeleteIfExists(filepath.Join(targetPath, "Samples~.meta"))
		if err := removeSamplesFromPackageJSON(filepath.Join(targetPath, "package.json")); err != nil {
			fmt.Printf("Warning: failed to remove 'samples' from package.json: %v\n", err)
		}
	}

	if includeScreenshots {
		if err := os.MkdirAll(filepath.Join(targetPath, "Screenshots~"), 0o755); err != nil {
			fmt.Printf("Warning: failed to create Screenshots~ folder: %v\n", err)
		}
	} else {
		tryDeleteIfExists(filepath.Join(targetPath, "Screenshots~"))
		tryDeleteIfExists(filepath.Join(targetPath, "Screenshots~.meta"))
	}

	if promptYesNo(reader, "Regenerate GUIDs in all .meta files? (y/n) [n]: ", false) {
		updated, err := regenerateMetaGUIDs(targetPath)
		if err != nil {
			fmt.Printf("Warning: GUID regeneration completed with errors: %v\n", err)
		}
		fmt.Printf("Regenerated GUIDs in %d .meta files.\n", updated)
	}

	fmt.Printf("Created template: %s\n\n", targetPath)
}

func editTemplate(reader *bufio.Reader) {
	recents := filterExistingRecentPaths(loadRecentEditPaths())
	templatePath := promptEditPackagePath(reader, recents)
	fmt.Println()

	if abs, err := filepath.Abs(filepath.Clean(templatePath)); err == nil {
		templatePath = abs
	}

	if !dirExists(templatePath) {
		fmt.Println("Path does not exist.")
		fmt.Println()
		return
	}

	packageJSONPath := filepath.Join(templatePath, "package.json")
	changelogPath := filepath.Join(templatePath, "CHANGELOG.md")

	if !fileExists(packageJSONPath) {
		fmt.Printf("package.json not found at: %s\n\n", packageJSONPath)
		return
	}
	if !fileExists(changelogPath) {
		fmt.Printf("CHANGELOG.md not found at: %s\n\n", changelogPath)
		return
	}

	currentVer, err := readPackageJSONVersion(packageJSONPath)
	if err != nil {
		fmt.Printf("Warning: could not read current version: %v\n\n", err)
	} else {
		fmt.Printf("Current package version: %s\n\n", currentVer)
	}

	newVersion := promptAllowEmpty(reader, "Enter new version for package.json (e.g. 1.0.1, Enter to keep current): ", "")
	if newVersion != "" {
		defaultLabel := time.Now().UTC().Format("2006-01-02")
		label := promptAllowEmpty(reader, fmt.Sprintf("Enter changelog label after '-' (default: %s): ", defaultLabel), defaultLabel)

		if err := updatePackageJSONVersion(packageJSONPath, newVersion); err != nil {
			fmt.Printf("Failed to update package.json version: %v\n\n", err)
			return
		}
		if err := prependChangelogVersion(changelogPath, newVersion, label); err != nil {
			fmt.Printf("Failed to update changelog: %v\n\n", err)
			return
		}
	}

	if promptYesNo(reader, "Add missing .meta files (new files get random GUIDs)? (y/n) [n]: ", false) {
		created, err := addMissingMetaFiles(templatePath)
		if err != nil {
			fmt.Printf("Warning: adding missing .meta files completed with errors: %v\n", err)
		}
		fmt.Printf("Created %d missing .meta file(s).\n", created)
	}

	if promptYesNo(reader, "Regenerate GUIDs in all .meta files? (y/n) [n]: ", false) {
		updated, err := regenerateMetaGUIDs(templatePath)
		if err != nil {
			fmt.Printf("Warning: GUID regeneration completed with errors: %v\n", err)
		}
		fmt.Printf("Regenerated GUIDs in %d .meta files.\n", updated)
	}

	if err := prependRecentEditPath(templatePath); err != nil {
		fmt.Printf("Warning: could not save recent paths: %v\n", err)
	}

	if newVersion != "" {
		fmt.Println("Template updated (version + changelog).")
	} else {
		fmt.Println("Edit finished (version and changelog unchanged).")
	}
	fmt.Println()
}

func runValidatePackage(reader *bufio.Reader) {
	recents := filterExistingRecentPaths(loadRecentEditPaths())
	templatePath := promptEditPackagePath(reader, recents)
	fmt.Println()

	if abs, err := filepath.Abs(filepath.Clean(templatePath)); err == nil {
		templatePath = abs
	}

	if !dirExists(templatePath) {
		fmt.Println("Path does not exist.")
		fmt.Println()
		return
	}

	findings := validatePackageLayout(templatePath)
	errCount, warnCount := 0, 0
	for _, f := range findings {
		switch f.severity {
		case severityError:
			fmt.Printf("ERROR: %s\n", f.message)
			errCount++
		case severityWarn:
			fmt.Printf("WARN: %s\n", f.message)
			warnCount++
		}
	}
	fmt.Printf("\nSummary: %d error(s), %d warning(s)\n", errCount, warnCount)
	fmt.Println()

	if errCount > 0 {
		os.Exit(1)
	}
}

func validatePackageLayout(root string) []validationFinding {
	var findings []validationFinding
	rootClean := filepath.Clean(root)
	packageJSONPath := filepath.Join(rootClean, "package.json")

	if !fileExists(packageJSONPath) {
		findings = append(findings, validationFinding{
			severityError,
			fmt.Sprintf("package.json not found at %s", packageJSONPath),
		})
		return findings
	}

	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		findings = append(findings, validationFinding{
			severityError,
			fmt.Sprintf("cannot read package.json: %v", err),
		})
		return findings
	}

	var manifest packageManifestForValidate
	if err := json.Unmarshal(data, &manifest); err != nil {
		findings = append(findings, validationFinding{
			severityError,
			fmt.Sprintf("package.json is not valid JSON: %v", err),
		})
		return findings
	}

	name := strings.TrimSpace(manifest.Name)
	if name == "" {
		findings = append(findings, validationFinding{
			severityError,
			`'name' in package.json is missing or empty`,
		})
	} else if !packageNamePattern.MatchString(name) {
		findings = append(findings, validationFinding{
			severityError,
			fmt.Sprintf(`package.json 'name' must match lowercase DNS-like pattern ^[a-z0-9][a-z0-9.-]*$ (got %q)`, name),
		})
	} else {
		folderBase := filepath.Base(rootClean)
		lastSeg := packageNameLastSegment(name)
		if !strings.EqualFold(folderBase, name) && !strings.EqualFold(folderBase, lastSeg) {
			findings = append(findings, validationFinding{
				severityWarn,
				fmt.Sprintf("folder name %q does not match package name %q (expected folder %q or %q, case-insensitive)", folderBase, name, lastSeg, name),
			})
		}
	}

	changelogPath := filepath.Join(rootClean, "CHANGELOG.md")
	if !fileExists(changelogPath) {
		findings = append(findings, validationFinding{
			severityWarn,
			fmt.Sprintf("CHANGELOG.md not found at %s", changelogPath),
		})
	}

	for i, sample := range manifest.Samples {
		p := strings.TrimSpace(sample.Path)
		if p == "" {
			findings = append(findings, validationFinding{
				severityWarn,
				fmt.Sprintf(`samples[%d] has missing or empty "path"`, i),
			})
			continue
		}
		full := filepath.Join(rootClean, filepath.FromSlash(p))
		if !dirExists(full) && !fileExists(full) {
			findings = append(findings, validationFinding{
				severityWarn,
				fmt.Sprintf(`samples[%d] path does not exist on disk: %s`, i, full),
			})
		}
	}

	findings = append(findings, orphanMetaFindings(rootClean)...)

	return findings
}

func packageNameLastSegment(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}

func orphanMetaFindings(root string) []validationFinding {
	var findings []validationFinding
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		if info.IsDir() && strings.HasPrefix(base, ".") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".meta") {
			return nil
		}
		assetPath := strings.TrimSuffix(path, ".meta")
		if _, statErr := os.Stat(assetPath); statErr != nil {
			findings = append(findings, validationFinding{
				severityWarn,
				fmt.Sprintf("orphan .meta (asset missing): %s", path),
			})
		}
		return nil
	})
	return findings
}

// promptEditPackagePath lists recents with 1-based indices; user may enter a number, a path, or blank (default first recent).
func promptEditPackagePath(reader *bufio.Reader, recents []string) string {
	if len(recents) == 0 {
		return promptNonEmpty(reader, "Enter existing template path: ")
	}
	fmt.Println("Recent package folders:")
	for i, p := range recents {
		fmt.Printf("  %d) %s\n", i+1, p)
	}
	fmt.Println()
	prompt := fmt.Sprintf("Enter path, or number 1-%d (default: %s): ", len(recents), recents[0])
	fmt.Print(prompt)
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return recents[0]
	}
	if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(recents) {
		return recents[n-1]
	}
	return text
}

func promptNonEmpty(reader *bufio.Reader, prompt string) string {
	for {
		fmt.Print(prompt)
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
		fmt.Println("Value cannot be empty.")
	}
}

func promptAllowEmpty(reader *bufio.Reader, prompt, defaultValue string) string {
	fmt.Print(prompt)
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return defaultValue
	}
	return text
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	for {
		fmt.Print(prompt)
		text, _ := reader.ReadString('\n')
		value := strings.ToLower(strings.TrimSpace(text))
		if value == "" {
			return defaultYes
		}
		if value == "y" || value == "yes" {
			return true
		}
		if value == "n" || value == "no" {
			return false
		}
		fmt.Println("Please enter 'y' or 'n'.")
	}
}

func copyDirectory(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func replaceTokenInDirectory(rootPath, oldValue, newValue string) error {
	if oldValue == "" {
		return nil
	}

	var paths []string
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		if !info.IsDir() {
			tryReplaceInTextFile(path, oldValue, newValue)
		}
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, path := range paths {
		base := filepath.Base(path)
		if !strings.Contains(base, oldValue) {
			continue
		}
		newName := strings.ReplaceAll(base, oldValue, newValue)
		newPath := filepath.Join(filepath.Dir(path), newName)
		if newPath == path {
			continue
		}
		_ = os.Rename(path, newPath)
	}

	return nil
}

func applyTemplateTokenReplacements(rootPath, currentName, newName, newAuthor, currentAuthor string) error {
	if err := replaceTokenInDirectory(rootPath, currentName, newName); err != nil {
		return err
	}
	if err := replaceTokenInDirectory(rootPath, toKebabCase(currentName), toKebabCase(newName)); err != nil {
		return err
	}
	if err := replaceTokenInDirectory(rootPath, toNoSpace(currentName), toNoSpace(newName)); err != nil {
		return err
	}
	if err := replaceTokenInDirectory(rootPath, "upm-token", toKebabCase(newName)); err != nil {
		return err
	}
	if err := replaceTokenInDirectory(rootPath, "UPMTemplate", toNoSpace(newName)); err != nil {
		return err
	}
	if currentAuthor != "" {
		if err := replaceTokenInDirectory(rootPath, currentAuthor, newAuthor); err != nil {
			return err
		}
	}
	if err := replaceTokenInDirectory(rootPath, "Author", newAuthor); err != nil {
		return err
	}
	replaceAuthorInLicenseFile(filepath.Join(rootPath, "LICENSE"), newAuthor)
	return nil
}

func replaceAuthorInLicenseFile(licensePath, newAuthor string) {
	if !fileExists(licensePath) {
		return
	}
	tryReplaceInTextFile(licensePath, "Author", newAuthor)
}

func toKebabCase(value string) string {
	parts := strings.Fields(value)
	return strings.ToLower(strings.Join(parts, "-"))
}

func toNoSpace(value string) string {
	return strings.ReplaceAll(value, " ", "")
}

func tryReplaceInTextFile(path, oldValue, newValue string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	if !strings.Contains(content, oldValue) {
		return
	}
	updated := strings.ReplaceAll(content, oldValue, newValue)
	_ = os.WriteFile(path, []byte(updated), 0o644)
}

func readPackageJSONVersion(packageJSONPath string) (string, error) {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return "", err
	}
	m := packageVersionPattern.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return "", fmt.Errorf("could not find 'version' key")
	}
	return m[1], nil
}

func recentEditsFilePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "upm-template-creator", "recent-edits.txt"), nil
}

func loadRecentEditPaths() []string {
	path, err := recentEditsFilePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		clean := filepath.Clean(line)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func filterExistingRecentPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if dirExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func prependRecentEditPath(templatePath string) error {
	absPath, err := filepath.Abs(filepath.Clean(templatePath))
	if err != nil {
		return err
	}
	filePath, err := recentEditsFilePath()
	if err != nil {
		return err
	}
	prev := loadRecentEditPaths()
	var merged []string
	merged = append(merged, absPath)
	for _, p := range prev {
		clean := filepath.Clean(p)
		if clean == absPath || !dirExists(clean) {
			continue
		}
		merged = append(merged, clean)
		if len(merged) >= maxRecentEditPaths {
			break
		}
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(strings.Join(merged, "\n")), 0o644)
}

func updatePackageJSONVersion(packageJSONPath, newVersion string) error {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return err
	}
	content := string(data)
	re := regexp.MustCompile(`"version"\s*:\s*"[^"]*"`)
	if !re.MatchString(content) {
		return fmt.Errorf("could not find 'version' key")
	}
	updated := re.ReplaceAllString(content, fmt.Sprintf(`"version": "%s"`, newVersion))
	return os.WriteFile(packageJSONPath, []byte(updated), 0o644)
}

func removeSamplesFromPackageJSON(packageJSONPath string) error {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return err
	}
	content := string(data)

	re := regexp.MustCompile(`(,\s*"samples"\s*:\s*\[[\s\S]*?\])|("samples"\s*:\s*\[[\s\S]*?\]\s*,?)`)
	updated := re.ReplaceAllString(content, "")
	trailingCommaRe := regexp.MustCompile(`,\s*(\}|\])`)
	updated = trailingCommaRe.ReplaceAllString(updated, `$1`)

	return os.WriteFile(packageJSONPath, []byte(updated), 0o644)
}

func prependChangelogVersion(changelogPath, version, label string) error {
	data, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	content := string(data)

	newSection := fmt.Sprintf(
		"## [%s] - %s\n\n### Added\n-\n\n### Fixed\n-\n\n### Changed\n-\n\n### Removed\n-\n\n",
		version,
		label,
	)

	insertIndex := strings.Index(content, "\n## [")
	if insertIndex < 0 {
		insertIndex = 0
	} else {
		insertIndex++
	}

	if strings.Contains(content, fmt.Sprintf("## [%s] - ", version)) {
		fmt.Printf("Warning: changelog already contains version '%s'. Prepending anyway.\n", version)
	}

	updated := content[:insertIndex] + newSection + "\n" + content[insertIndex:]
	return os.WriteFile(changelogPath, []byte(updated), 0o644)
}

func addMissingMetaFiles(rootPath string) (int, error) {
	created := 0
	var firstErr error
	rootClean := filepath.Clean(rootPath)

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}

		if filepath.Clean(path) == rootClean {
			return nil
		}

		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasSuffix(strings.ToLower(path), ".meta") {
			return nil
		}

		if shouldOmitMetaForAsset(rootClean, path, info) {
			return nil
		}

		metaPath := path + ".meta"
		if fileExists(metaPath) || dirExists(metaPath) {
			return nil
		}

		body := metaContentForPath(path, info.IsDir())
		if writeErr := os.WriteFile(metaPath, []byte(body), 0o644); writeErr != nil {
			if firstErr == nil {
				firstErr = writeErr
			}
			return nil
		}
		created++
		return nil
	})
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return created, firstErr
}

// relPathHasTildeDir reports whether rel (relative to package root) passes through a directory
// whose name ends with '~' (Unity optional-folder convention).
func relPathHasTildeDir(rel string, isDir bool) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return false
	}
	parts := strings.Split(rel, "/")
	limit := len(parts)
	if !isDir && limit > 0 {
		limit--
	}
	for i := 0; i < limit; i++ {
		if strings.HasSuffix(parts[i], "~") {
			return true
		}
	}
	return false
}

func shouldOmitMetaForAsset(rootClean, assetPath string, info os.FileInfo) bool {
	if filepath.Clean(assetPath) == rootClean {
		return false
	}
	rel, err := filepath.Rel(rootClean, filepath.Clean(assetPath))
	if err != nil {
		return false
	}
	if relPathHasTildeDir(rel, info.IsDir()) {
		return true
	}
	return false
}

func metaContentForPath(assetPath string, isDir bool) string {
	guid := randomGUID32()
	base := strings.ToLower(filepath.Base(assetPath))
	ext := strings.ToLower(filepath.Ext(assetPath))

	if isDir {
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
folderAsset: yes
DefaultImporter:
  externalObjects: {}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	}

	switch ext {
	case ".cs":
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
MonoImporter:
  externalObjects: {}
  defaultReferences: []
  executionOrder: 0
  icon: {instanceID: 0}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	case ".asmdef":
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
AssemblyDefinitionImporter:
  externalObjects: {}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	case ".prefab", ".unity", ".asset", ".physicMaterial", ".mat", ".controller", ".overrideController", ".mask", ".anim":
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
NativeFormatImporter:
  externalObjects: {}
  mainObjectFileID: 0
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	case ".dll", ".so", ".bundle":
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
PluginImporter:
  externalObjects: {}
  isPreloaded: 0
  isOverridable: 0
  isExplicitlyReferenced: 0
  validateReferences: 1
  platformData: []
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	case ".png", ".jpg", ".jpeg", ".tga", ".psd", ".gif", ".bmp":
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
TextureImporter:
  externalObjects: {}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	default:
		if ext == ".json" || ext == ".md" || ext == ".txt" || ext == ".xml" || ext == ".yaml" || ext == ".yml" ||
			base == "license" || strings.HasPrefix(base, "license.") {
			return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
TextScriptImporter:
  externalObjects: {}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
		}
		return fmt.Sprintf(`fileFormatVersion: 2
guid: %s
DefaultImporter:
  externalObjects: {}
  userData: 
  assetBundleName: 
  assetBundleVariant: 
`, guid)
	}
}

func regenerateMetaGUIDs(rootPath string) (int, error) {
	re := regexp.MustCompile(`(?m)^guid:\s*[0-9a-fA-F]+\s*$`)
	updatedCount := 0
	var firstErr error
	rootClean := filepath.Clean(rootPath)

	_ = filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if info.IsDir() || filepath.Ext(path) != ".meta" {
			return nil
		}

		assetPath := strings.TrimSuffix(path, ".meta")
		st, statErr := os.Stat(assetPath)
		if statErr != nil {
			return nil
		}
		if shouldOmitMetaForAsset(rootClean, assetPath, st) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if firstErr == nil {
				firstErr = readErr
			}
			return nil
		}
		content := string(data)
		if !re.MatchString(content) {
			return nil
		}

		newGUID := randomGUID32()
		updated := re.ReplaceAllString(content, "guid: "+newGUID)
		if writeErr := os.WriteFile(path, []byte(updated), 0o644); writeErr != nil {
			if firstErr == nil {
				firstErr = writeErr
			}
			return nil
		}
		updatedCount++
		return nil
	})

	return updatedCount, firstErr
}

func randomGUID32() string {
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	if err != nil {
		return strings.Repeat("0", 32)
	}
	return hex.EncodeToString(bytes)
}

func resolveTemplatePath() string {
	cwd, _ := os.Getwd()
	exeDir, _ := os.Executable()
	baseDir := filepath.Dir(exeDir)

	var candidates []string
	candidates = append(candidates, filepath.Join(cwd, templateToken))
	candidates = append(candidates, filepath.Join(baseDir, templateToken))

	projectDir := filepath.Dir(cwd)
	candidates = append(candidates, filepath.Join(projectDir, templateToken))

	for _, c := range candidates {
		if dirExists(c) {
			return c
		}
	}
	return ""
}

func getSuggestedDestinationFolder(templatePath string) string {
	parent := filepath.Dir(templatePath)
	if parent == "" || parent == "." {
		cwd, _ := os.Getwd()
		return cwd
	}
	return parent
}

func tryDeleteIfExists(path string) {
	if fileExists(path) {
		_ = os.Remove(path)
		return
	}
	if dirExists(path) {
		_ = os.RemoveAll(path)
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
