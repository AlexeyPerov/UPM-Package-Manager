# UPM Template Creator

Simple console app to create and/or edit a Unity Package Manager plugin template. Here you can create a new package, edit existing one, maintain versions and guids in `.meta` files.

## Requirements

- [Go](https://go.dev/) 1.18+

## Run

From this repository root:

```bash
go run upm_template_creator.go
```

## Usage

The app has following options:

1. **Create a new package** — Copies the template (`UPM-Template` directory) into `<destination>/<package name>`, replaces name/author tokens (`UPM-Template`, `upm-token`, `UPMTemplate`, `Author`, etc.), optionally drops `Roadmap.md` and `Samples~` (and the `samples` block in `package.json` if samples are removed), and optionally regenerates all `.meta` GUIDs.
2. **Edit an existing package** — Asks for the package folder path, bumps `version` in `package.json`, prepends a section to `CHANGELOG.md`, and optionally adds missing `.meta` files or regenerates all `.meta` GUIDs.

