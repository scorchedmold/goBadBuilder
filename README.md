# goBadBuilder

goBadBuilder is a cross-platform CLI rewrite of BadBuilder for preparing an Xbox 360 BadUpdate USB drive.

This version intentionally does not include:

- USB formatting
- Windows GUI behavior
- the old custom FAT32 formatter

Format the USB drive yourself first, then point goBadBuilder at the mounted USB root.

## Requirements

- Go 1.22 or newer to build from source
- `7zz`, `7z`, or `7za` on your `PATH` for `.7z` extraction
- Windows, or Wine on macOS/Linux, for XexTool patching

The downloaded BadUpdate tools include `XexTool.exe`. On Windows it runs directly. On macOS/Linux, goBadBuilder will use `wine` if it is installed.

## Build

```sh
go build -o gobadbuilder .
```

## Run

Interactive mode:

```sh
./gobadbuilder
```

With a known USB root:

```sh
./gobadbuilder --target /Volumes/BADUPDATE
```

With a chosen default payload:

```sh
./gobadbuilder --target /Volumes/BADUPDATE --default-app FreeMyXe
```

Valid default payloads are `FreeMyXe` and `XeUnshackle`.

## Workflow

1. Confirm the mounted USB root directory.
2. Choose the default BadUpdate payload.
3. Download or reuse required archives.
4. Extract archives into `Work/Extract`.
5. Copy the BadUpdate files to the USB root.
6. Optionally copy homebrew apps into `Apps/`.
7. Patch copied `.xex` files with XexTool.

goBadBuilder writes `name.txt` and `info.txt` to the USB root so you can confirm what was created.

## Notes

The tool copies and patches homebrew entry points on the USB copy, not in the original source folder.

The app does not delete or format anything outside of its working extraction folder. It does overwrite files at the selected USB root when those paths are part of the BadUpdate layout.
