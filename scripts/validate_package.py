#!/usr/bin/env python3
from __future__ import annotations

import json
import re
import stat
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "package" / "ha500-toolbox"
SEMVER = re.compile(r"^\d+\.\d+\.\d+$")
GO_VERSION = re.compile(r"^go(\d+)\.(\d+)\.(\d+)$")


def ensure(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(f"validation error: {message}")


def manifest() -> dict[str, str]:
    result: dict[str, str] = {}
    for number, raw in enumerate((PACKAGE / "manifest").read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        ensure("=" in line, f"manifest:{number} is not key=value")
        key, value = (part.strip() for part in line.split("=", 1))
        ensure(key not in result, f"manifest duplicates {key}")
        result[key] = value
    return result


def go_version(binary: Path) -> tuple[int, int, int]:
    result = subprocess.run(
        ["go", "version", "-m", str(binary)],
        check=True,
        capture_output=True,
        text=True,
    )
    first = result.stdout.splitlines()[0]
    _, separator, version = first.partition(": ")
    ensure(bool(separator), "cannot inspect Go toolchain")
    match = GO_VERSION.fullmatch(version.strip())
    ensure(match is not None, f"unexpected Go version {version.strip()}")
    return tuple(int(part) for part in match.groups())


def main() -> int:
    required = (
        "manifest",
        "ICON.PNG",
        "ICON_256.PNG",
        "app/bin/ha500-toolbox",
        "app/ui/config",
        "app/ui/images/icon_64.png",
        "app/ui/images/icon_256.png",
        "config/privilege",
        "config/resource",
        "wizard/install",
        "cmd/main",
        "cmd/install_init",
        "cmd/install_callback",
        "cmd/uninstall_init",
        "cmd/uninstall_callback",
        "cmd/upgrade_init",
        "cmd/upgrade_callback",
        "cmd/config_init",
        "cmd/config_callback",
    )
    for relative in required:
        ensure((PACKAGE / relative).is_file(), f"missing {relative}")

    metadata = manifest()
    ensure(metadata.get("appname") == "ha500_toolbox", "unexpected appname")
    ensure(metadata.get("display_name") == "HA500 飞牛工具箱", "unexpected display name")
    ensure(SEMVER.fullmatch(metadata.get("version", "")) is not None, "invalid version")
    ensure(metadata.get("arch") == "x86_64", "first release must target HA500 x86_64 only")
    ensure("install_dep_apps" not in metadata, "Go package must not depend on Python")

    for relative in ("app/ui/config", "config/privilege", "config/resource", "wizard/install"):
        json.loads((PACKAGE / relative).read_text(encoding="utf-8"))
    privilege = json.loads((PACKAGE / "config/privilege").read_text(encoding="utf-8"))
    ensure(privilege.get("defaults", {}).get("run-as") == "root", "hardware service must run as root")
    ui = json.loads((PACKAGE / "app/ui/config").read_text(encoding="utf-8"))
    launch = ui.get(".url", {}).get(metadata.get("desktop_applaunchname", ""), {})
    ensure(launch.get("gatewayPrefix") == "/app/ha500_toolbox", "incorrect gatewayPrefix")
    ensure(launch.get("gatewaySocket") == "app.sock", "incorrect gateway socket")
    ensure(launch.get("allUsers") is False, "toolbox must remain administrator-only")

    for command in (PACKAGE / "cmd").iterdir():
        if command.is_file():
            ensure(bool(command.stat().st_mode & stat.S_IXUSR), f"{command.name} is not executable")
    binary = PACKAGE / "app/bin/ha500-toolbox"
    ensure(bool(binary.stat().st_mode & stat.S_IXUSR), "service binary is not executable")
    ensure(go_version(binary) >= (1, 25, 12), "service must use Go 1.25.12 or newer")

    sources = [
        *(ROOT / "cmd").rglob("*.go"),
        *(ROOT / "internal").rglob("*"),
        *(PACKAGE / "cmd").rglob("*"),
        *(PACKAGE / "config").rglob("*"),
        *(PACKAGE / "app" / "ui").rglob("*"),
        *(PACKAGE / "wizard").rglob("*"),
        PACKAGE / "manifest",
        ROOT / "README.md",
    ]
    text = "\n".join(
        path.read_text(encoding="utf-8", errors="ignore")
        for path in sources
        if path.is_file() and path.suffix.lower() not in {".png", ".jpg", ".jpeg"}
    )
    ensure(
        re.search(r"(?i)(?:password|token|secret)\s*[=:]\s*['\"][^'\"]+['\"]", text) is None,
        "hard-coded credential found",
    )
    ensure(
        re.search(r"\b(?:10(?:\.\d{1,3}){3}|192\.168(?:\.\d{1,3}){2}|172\.(?:1[6-9]|2\d|3[01])(?:\.\d{1,3}){2})\b", text) is None,
        "private deployment address found",
    )
    print(f"validated ha500_toolbox {metadata['version']}: {len(required)} required files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

