# UPM Package Manager

Simple console app to create and/or edit a Unity Package Manager plugin template. Here you can create a new package, edit existing one, validate layout, maintain versions and guids in `.meta` files.

## Requirements

- [Go](https://go.dev/) 1.18+

## Run

From this repository root:

```bash
go run upm_manager.go
```

## Usage

The app has following options:

1. Create a new plugin 

- copies the template (`UPM-Template` directory) into specified destination, 
- sets Author ,
- optionally drops `Roadmap.md`, `Samples~` and `Screenshots~`
- optionally regenerates all `.meta` GUIDs.

2. Edit an existing plugin

- asks for the package folder path, 
- increases `version` in `package.json`, 
- prepends a section to `CHANGELOG.md`, 
- and optionally adds missing `.meta` files or regenerates all `.meta` GUIDs.

3. Validate a package layout

- asks for the package folder path (same recent-folder shortcuts as Edit),
- reports **ERROR** issues (missing `package.json`, unreadable file, invalid JSON, missing or invalid `name` per Unity-style lowercase DNS-like pattern `^[a-z0-9][a-z0-9.-]*$`),
- reports **WARN** issues when applicable:
  - folder name does not match the package `name` (expects the root folder to match either the full `name` or its last dotted segment, case-insensitive),
  - `CHANGELOG.md` is missing,
  - each `samples[]` entry has a non-empty `path` that exists under the package root,
  - orphan `.meta` files (`.meta` present without the corresponding asset folder/file).

After validation finishes, the process exits with status **1** if there was at least one ERROR; warnings alone exit with **0**.
