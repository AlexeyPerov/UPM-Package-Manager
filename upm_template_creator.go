package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const templateToken = "UPM-Template"

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
		fmt.Println("3) Exit")
		choice := promptNonEmpty(reader, "Please enter your choice (1-3): ")
		fmt.Println()

		switch strings.TrimSpace(choice) {
		case "1":
			createTemplate(reader, defaultTemplatePath)
		case "2":
			editTemplate(reader)
		case "3":
			fmt.Println("Exiting..")
			return
		default:
			fmt.Println("Invalid option. Enter 1, 2, or 3.")
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
	templatePath := promptNonEmpty(reader, "Enter existing template path: ")
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

	newVersion := promptNonEmpty(reader, "Enter new version for package.json (e.g. 1.0.1): ")
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

	fmt.Println("Template updated (version + changelog).")
	fmt.Println()
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
	if !info.IsDir() && strings.EqualFold(filepath.Base(assetPath), "LICENSE") {
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
