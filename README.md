# UPM Package Manager

Simple console app to create and/or edit a Unity Package Manager plugin template: full `package.json` prompts matching the bundled template shape, validation, `.meta` maintenance, **known** package-folder shortcuts, optional **scan for packages**, and optional batch git actions.

## Requirements

- [Go](https://go.dev/) 1.18+
- **Batch commit/push**: `git` on `PATH`; each selected folder must be a git repo with a configured remote for `git push`.

## Run

From this repository root:

```bash
go run upm_manager.go
```

## Usage

The app has these options:

1. **Create a new package**

- Loads defaults from the template’s `package.json`, then asks for **Roadmap.md**, **Samples~**, and **Screenshots~** (yes/no).
- Prompts all manifest fields (Enter keeps the default shown): `name`, `version`, `displayName`, `description`, `unity`, `keywords` (comma-separated), `author.name`, `author.url`, and **samples** (`displayName`, `description`, `path` per entry) **only when Samples~ is included**.
- Copies `UPM-Template`, applies token replacements for README/asmdef/etc., drops optional folders, writes **`package.json`** from your answers, optionally regenerates `.meta` GUIDs.
- Adds the new package path to the **known packages** list.

2. **Edit an existing package**

- Chooses the package folder (known-folder shortcuts supported).
- Prompts the same **`package.json`** fields from current values (Enter keeps current).
- If **`version`** changes, asks for the changelog date label and prepends **`CHANGELOG.md`** (same layout as before).
- Optionally adds missing `.meta` files or regenerates GUIDs.
- Updates **known packages**.

3. **Validate a package layout**

- Chooses the package folder (same shortcuts as Edit).
- Reports **ERROR** / **WARN** issues (`package.json`, `name` pattern, folder vs name, `CHANGELOG.md`, `samples` paths, orphan `.meta`).
- Exits with status **1** if any ERROR; warnings-only exits **0**.

4. **Scan for packages**

- Asks for a root folder and inspects **only immediate child folders** (not deeper).
- Any subfolder that contains **`package.json`** is treated as a package; paths are merged into the **known packages** list (duplicates skipped, existing entries kept).

5. **Batch operations**

- Shows **known** package folders; select indices separated by commas or spaces (e.g. `1,3`), **`all`**, or blank to cancel.
- **Commit and push changes**: asks for a **commit message**. For each selected repo with local changes, prints **changed/untracked files**, then asks whether to **proceed** before running `git add -A`, `git commit`, and `git push`. Skips non-git repos, clean trees, user declines, or failures; prints one status line per folder and a short summary.

6. **Exit**
