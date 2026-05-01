package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	templateToken    = "UPM-Template"
	maxKnownPackages = 20
)

var packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

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

// PackageManifest matches Unity manifest shape used by UPM-Template/package.json.
type PackageManifest struct {
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	DisplayName string           `json:"displayName"`
	Description string           `json:"description"`
	Unity       string           `json:"unity"`
	Keywords    []string         `json:"keywords"`
	Author      manifestAuthor   `json:"author"`
	Samples     []manifestSample `json:"samples,omitempty"`
}

type manifestAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type manifestSample struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type manifestPromptOpts struct {
	create         bool
	includeSamples bool
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
		fmt.Println("4) Scan for packages")
		fmt.Println("5) Batch operations")
		fmt.Println("6) Exit")
		choice := promptNonEmpty(reader, "Please enter your choice (1-6): ")
		fmt.Println()

		switch strings.TrimSpace(choice) {
		case "1":
			createTemplate(reader, defaultTemplatePath)
		case "2":
			editTemplate(reader)
		case "3":
			runValidatePackage(reader)
		case "4":
			scanPackagesOneLevel(reader)
		case "5":
			runBatchOperations(reader)
		case "6":
			fmt.Println("Exiting..")
			return
		default:
			fmt.Println("Invalid option. Enter 1, 2, 3, 4, 5, or 6.")
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

	templatePackageJSON := filepath.Join(resolvedSource, "package.json")
	defaultManifest, err := readPackageManifest(templatePackageJSON)
	if err != nil {
		fmt.Printf("Cannot read template package.json (%s): %v\n\n", templatePackageJSON, err)
		return
	}

	pluginName := promptNonEmpty(reader, "Enter new package name (folder name): ")
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

	includeRoadmap := promptYesNo(reader, "Include 'Roadmap.md'? (y/n) [y]: ", true)
	includeSamples := promptYesNo(reader, "Include 'Samples~' folder? (y/n) [y]: ", true)
	includeScreenshots := promptYesNo(reader, "Create 'Screenshots~' folder? (y/n) [y]: ", true)

	fmt.Println()
	fmt.Println("package.json fields (defaults from template; Enter keeps default):")
	manifest := promptPackageManifest(reader, defaultManifest, manifestPromptOpts{
		create:         true,
		includeSamples: includeSamples,
	})
	if !includeSamples {
		manifest.Samples = nil
	}
	fmt.Println()

	if err := copyDirectory(resolvedSource, targetPath); err != nil {
		fmt.Printf("Failed to copy template: %v\n\n", err)
		return
	}
	if err := applyTemplateTokenReplacements(targetPath, templateToken, pluginName, manifest.Author.Name, ""); err != nil {
		fmt.Printf("Failed to apply replacements: %v\n\n", err)
		return
	}

	if !includeRoadmap {
		tryDeleteIfExists(filepath.Join(targetPath, "Roadmap.md"))
		tryDeleteIfExists(filepath.Join(targetPath, "Roadmap.md.meta"))
	}

	if !includeSamples {
		tryDeleteIfExists(filepath.Join(targetPath, "Samples~"))
		tryDeleteIfExists(filepath.Join(targetPath, "Samples~.meta"))
	}

	if includeScreenshots {
		if err := os.MkdirAll(filepath.Join(targetPath, "Screenshots~"), 0o755); err != nil {
			fmt.Printf("Warning: failed to create Screenshots~ folder: %v\n", err)
		}
	} else {
		tryDeleteIfExists(filepath.Join(targetPath, "Screenshots~"))
		tryDeleteIfExists(filepath.Join(targetPath, "Screenshots~.meta"))
	}

	packageJSONPath := filepath.Join(targetPath, "package.json")
	if err := writePackageManifest(packageJSONPath, manifest); err != nil {
		fmt.Printf("Failed to write package.json: %v\n\n", err)
		return
	}

	if promptYesNo(reader, "Regenerate GUIDs in all .meta files? (y/n) [n]: ", false) {
		updated, err := regenerateMetaGUIDs(targetPath)
		if err != nil {
			fmt.Printf("Warning: GUID regeneration completed with errors: %v\n", err)
		}
		fmt.Printf("Regenerated GUIDs in %d .meta files.\n", updated)
	}

	if err := prependKnownPackagePath(targetPath); err != nil {
		fmt.Printf("Warning: could not save known package paths: %v\n", err)
	}

	fmt.Printf("Created template: %s\n\n", targetPath)
}

func editTemplate(reader *bufio.Reader) {
	known := filterExistingKnownPaths(loadKnownPackagePaths())
	templatePath := promptPackageFolderPath(reader, known)
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

	manifest, err := readPackageManifest(packageJSONPath)
	if err != nil {
		fmt.Printf("Failed to read package.json: %v\n\n", err)
		return
	}

	prevVersion := strings.TrimSpace(manifest.Version)

	fmt.Println("package.json fields (Enter keeps current value):")
	updated := promptPackageManifest(reader, manifest, manifestPromptOpts{create: false})
	fmt.Println()

	if err := writePackageManifest(packageJSONPath, updated); err != nil {
		fmt.Printf("Failed to write package.json: %v\n\n", err)
		return
	}

	newVersion := strings.TrimSpace(updated.Version)
	if newVersion != prevVersion {
		defaultLabel := time.Now().UTC().Format("2006-01-02")
		label := promptAllowEmpty(reader, fmt.Sprintf("Enter changelog label after '-' (default: %s): ", defaultLabel), defaultLabel)
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
		updatedCount, err := regenerateMetaGUIDs(templatePath)
		if err != nil {
			fmt.Printf("Warning: GUID regeneration completed with errors: %v\n", err)
		}
		fmt.Printf("Regenerated GUIDs in %d .meta files.\n", updatedCount)
	}

	if err := prependKnownPackagePath(templatePath); err != nil {
		fmt.Printf("Warning: could not save known package paths: %v\n", err)
	}

	if newVersion != prevVersion {
		fmt.Println("Template updated (package.json + changelog).")
	} else {
		fmt.Println("Edit finished (package.json saved; version and changelog unchanged).")
	}
	fmt.Println()
}

func runValidatePackage(reader *bufio.Reader) {
	known := filterExistingKnownPaths(loadKnownPackagePaths())
	templatePath := promptPackageFolderPath(reader, known)
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

func scanPackagesOneLevel(reader *bufio.Reader) {
	root := strings.TrimSpace(promptNonEmpty(reader, "Enter folder to scan (immediate subfolders only): "))
	root = filepath.Clean(root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Printf("Invalid path: %v\n\n", err)
		return
	}
	root = absRoot

	if !dirExists(root) {
		fmt.Println("Folder does not exist.")
		fmt.Println()
		return
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Printf("Cannot read folder: %v\n\n", err)
		return
	}

	var found []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		sub := filepath.Join(root, name)
		if fileExists(filepath.Join(sub, "package.json")) {
			if abs, err := filepath.Abs(sub); err == nil {
				found = append(found, abs)
			}
		}
	}
	sort.Strings(found)
	if len(found) == 0 {
		fmt.Println("No packages found (no immediate subfolder contains package.json).")
		fmt.Println()
		return
	}

	fmt.Printf("Found %d package(s):\n", len(found))
	for _, p := range found {
		fmt.Printf("  %s\n", p)
	}
	fmt.Println()

	if err := mergeKnownPackagesAtFront(found); err != nil {
		fmt.Printf("Warning: could not save known packages: %v\n", err)
	} else {
		fmt.Println("Merged into known packages list (duplicates skipped).")
	}
	fmt.Println()
}

func runBatchOperations(reader *bufio.Reader) {
	known := filterExistingKnownPaths(loadKnownPackagePaths())
	if len(known) == 0 {
		fmt.Println("No known package folders saved yet.")
		fmt.Println()
		return
	}

	fmt.Println("Known package folders:")
	for i, p := range known {
		fmt.Printf("  %d) %s\n", i+1, p)
	}
	fmt.Println()

	fmt.Printf("Select indices (comma/space-separated), \"all\", or blank to cancel (1-%d): ", len(known))
	line, _ := reader.ReadString('\n')
	indices, err := parseMultiSelect(strings.TrimSpace(line), len(known))
	if err != nil {
		fmt.Printf("Invalid selection: %v\n\n", err)
		return
	}
	if len(indices) == 0 {
		fmt.Println("Cancelled (no packages selected).")
		fmt.Println()
		return
	}

	fmt.Println()
	fmt.Println("Batch action:")
	fmt.Println("1) Commit and push changes")
	fmt.Println("2) Cancel")
	action := strings.TrimSpace(promptAllowEmpty(reader, "Choice (1-2, default 2): ", "2"))
	if action != "1" {
		fmt.Println("Cancelled.")
		fmt.Println()
		return
	}

	msg := promptNonEmpty(reader, "Commit message: ")
	fmt.Println()

	fmt.Println("Results:")
	var summaries []string
	for _, idx := range indices {
		dir := known[idx-1]
		result := batchGitCommitPush(reader, dir, msg)
		lineSummary := fmt.Sprintf("%s — %s", dir, result)
		summaries = append(summaries, lineSummary)
		fmt.Println(lineSummary)
	}
	fmt.Println()
	fmt.Println("Batch summary:")
	for _, s := range summaries {
		fmt.Println(" ", s)
	}
	fmt.Println()
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

// promptPackageFolderPath lists known paths with 1-based indices; user may enter a number, a path, or blank (default first known).
func promptPackageFolderPath(reader *bufio.Reader, knownPackages []string) string {
	if len(knownPackages) == 0 {
		return promptNonEmpty(reader, "Enter existing template path: ")
	}
	fmt.Println("Known package folders:")
	for i, p := range knownPackages {
		fmt.Printf("  %d) %s\n", i+1, p)
	}
	fmt.Println()
	prompt := fmt.Sprintf("Enter path, or number 1-%d (default: %s): ", len(knownPackages), knownPackages[0])
	fmt.Print(prompt)
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return knownPackages[0]
	}
	if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(knownPackages) {
		return knownPackages[n-1]
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

func clonePackageManifest(m *PackageManifest) PackageManifest {
	if m == nil {
		return PackageManifest{}
	}
	out := *m
	out.Keywords = append([]string(nil), m.Keywords...)
	out.Samples = append([]manifestSample(nil), m.Samples...)
	return out
}

func readPackageManifest(path string) (*PackageManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m PackageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func writePackageManifest(path string, m *PackageManifest) error {
	toWrite := clonePackageManifest(m)
	if toWrite.Keywords == nil {
		toWrite.Keywords = []string{}
	}
	data, err := json.MarshalIndent(toWrite, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func keywordsEditString(k []string) string {
	return strings.Join(k, ", ")
}

func parseKeywords(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parts := strings.Split(line, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func promptPackageManifest(reader *bufio.Reader, defaults *PackageManifest, opts manifestPromptOpts) *PackageManifest {
	cloned := clonePackageManifest(defaults)
	out := &cloned

	for {
		def := out.Name
		name := strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("name [%s]: ", def), def))
		if opts.create && name == "" {
			fmt.Println("Name cannot be empty.")
			continue
		}
		out.Name = name
		if out.Name != "" && !packageNamePattern.MatchString(out.Name) {
			fmt.Printf("Invalid package name %q (expected pattern ^[a-z0-9][a-z0-9.-]*$).\n", out.Name)
			continue
		}
		break
	}

	def := strings.TrimSpace(out.Version)
	out.Version = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("version [%s]: ", def), def))

	def = out.DisplayName
	out.DisplayName = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("displayName [%s]: ", def), def))

	def = out.Description
	out.Description = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("description [%s]: ", def), def))

	def = out.Unity
	out.Unity = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("unity [%s]: ", def), def))

	kwDef := keywordsEditString(out.Keywords)
	kwLine := strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("keywords (comma-separated) [%s]: ", kwDef), kwDef))
	out.Keywords = parseKeywords(kwLine)

	def = out.Author.Name
	out.Author.Name = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("author.name [%s]: ", def), def))

	def = out.Author.URL
	out.Author.URL = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("author.url [%s]: ", def), def))

	promptSamples := false
	if opts.create && opts.includeSamples {
		promptSamples = true
		if len(out.Samples) == 0 {
			out.Samples = []manifestSample{{}}
		}
	} else if !opts.create && len(out.Samples) > 0 {
		promptSamples = true
	}

	if promptSamples {
		for i := range out.Samples {
			fmt.Printf("samples[%d]\n", i)
			s := &out.Samples[i]
			def = s.DisplayName
			s.DisplayName = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("  displayName [%s]: ", def), def))
			def = s.Description
			s.Description = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("  description [%s]: ", def), def))
			def = s.Path
			s.Path = strings.TrimSpace(promptAllowEmpty(reader, fmt.Sprintf("  path [%s]: ", def), def))
		}
	}

	return out
}

func parseMultiSelect(line string, max int) ([]int, error) {
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return nil, nil
	}
	if line == "all" {
		out := make([]int, max)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	line = strings.ReplaceAll(line, ",", " ")
	fields := strings.Fields(line)
	seen := make(map[int]struct{})
	var nums []int
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("invalid token %q", f)
		}
		if n < 1 || n > max {
			return nil, fmt.Errorf("index %d out of range (1-%d)", n, max)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums, nil
}

func gitIsRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func gitChangedPaths(dir string) ([]string, error) {
	cmds := [][]string{
		{"git", "-C", dir, "diff", "--name-only"},
		{"git", "-C", dir, "diff", "--cached", "--name-only"},
		{"git", "-C", dir, "ls-files", "--others", "--exclude-standard"},
	}
	seen := make(map[string]struct{})
	var names []string
	for _, argv := range cmds {
		out, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			names = append(names, line)
		}
	}
	sort.Strings(names)
	return names, nil
}

func batchGitCommitPush(reader *bufio.Reader, dir, msg string) string {
	if !gitIsRepo(dir) {
		return "skipped (not a git repository)"
	}

	statusOut, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Sprintf("skipped (git status failed: %v)", err)
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return "skipped (working tree clean)"
	}

	paths, err := gitChangedPaths(dir)
	if err != nil {
		return fmt.Sprintf("skipped (could not list changed files: %v)", err)
	}
	fmt.Printf("Changed/untracked files in %s:\n", dir)
	if len(paths) == 0 {
		fmt.Println("  (could not enumerate paths; git add -A will still stage everything)")
	} else {
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	fmt.Println()
	if !promptYesNo(reader, "Proceed with commit and push for this repository? (y/n) [n]: ", false) {
		return "skipped (cancelled by user)"
	}

	add := exec.Command("git", "-C", dir, "add", "-A")
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		return fmt.Sprintf("failed (git add: %v)", err)
	}

	commit := exec.Command("git", "-C", dir, "commit", "-m", msg)
	var commitErr bytes.Buffer
	commit.Stderr = &commitErr
	if err := commit.Run(); err != nil {
		se := strings.ToLower(commitErr.String())
		if strings.Contains(se, "nothing to commit") {
			return "skipped (nothing to commit)"
		}
		return fmt.Sprintf("failed (git commit: %v — %s)", err, strings.TrimSpace(commitErr.String()))
	}

	push := exec.Command("git", "-C", dir, "push")
	var pushErr bytes.Buffer
	push.Stderr = &pushErr
	if err := push.Run(); err != nil {
		return fmt.Sprintf("failed (git push: %v — %s)", err, strings.TrimSpace(pushErr.String()))
	}

	return "ok (committed and pushed)"
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

func migrateLegacyRecentEditsFile() {
	newPath, err := knownPackagesFilePath()
	if err != nil || fileExists(newPath) {
		return
	}
	oldPath, err := legacyRecentEditsFilePath()
	if err != nil || !fileExists(oldPath) {
		return
	}
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(newPath, data, 0o644); err != nil {
		return
	}
	_ = os.Remove(oldPath)
}

func knownPackagesFilePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "upm-template-creator", "known-packages.txt"), nil
}

func legacyRecentEditsFilePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "upm-template-creator", "recent-edits.txt"), nil
}

func loadKnownPackagePathsFromFile(path string) []string {
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

func loadKnownPackagePaths() []string {
	migrateLegacyRecentEditsFile()
	path, err := knownPackagesFilePath()
	if err != nil {
		return nil
	}
	return loadKnownPackagePathsFromFile(path)
}

func filterExistingKnownPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if dirExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func mergeKnownPackagesAtFront(extra []string) error {
	migrateLegacyRecentEditsFile()
	filePath, err := knownPackagesFilePath()
	if err != nil {
		return err
	}
	prev := loadKnownPackagePathsFromFile(filePath)
	seen := make(map[string]struct{})
	var merged []string

	addAbs := func(p string) bool {
		abs, err := filepath.Abs(filepath.Clean(p))
		if err != nil || !dirExists(abs) {
			return false
		}
		if _, ok := seen[abs]; ok {
			return false
		}
		seen[abs] = struct{}{}
		merged = append(merged, abs)
		return true
	}

	for _, p := range extra {
		addAbs(p)
	}
	for _, p := range prev {
		if len(merged) >= maxKnownPackages {
			break
		}
		addAbs(p)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(strings.Join(merged, "\n")), 0o644)
}

func prependKnownPackagePath(packageDir string) error {
	return mergeKnownPackagesAtFront([]string{packageDir})
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
