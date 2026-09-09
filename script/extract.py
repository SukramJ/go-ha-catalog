#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 go-ha-catalog authors.
"""Extract the Home Assistant MQTT-discovery vocabulary into JSON artifacts.

Two stages, deliberately kept apart:

  Stage 0  reads the JSON that Home Assistant itself generates and whose
           freshness `hassfest` enforces in HA's own CI. No Python import of
           `homeassistant` is needed, so this stage cannot break on an HA
           refactor.

  Stage 1  imports the `homeassistant` package and introspects it: the
           StrEnum/IntFlag vocabularies, the relational tables, and — the point
           of the whole exercise — the per-platform MQTT ``DISCOVERY_SCHEMA``,
           which is the authoritative list of legal JSON keys per platform.

Run it through ``script/regenerate.sh``; that script owns the venv and the
stamping of the snapshot constants.

Two traps this file exists to navigate, both of which fail *silently*:

  * HA 2026.x swapped voluptuous for `probatio`. ``homeassistant/__init__.py``
    calls ``install_as_voluptuous()`` and aliases ``voluptuous`` in
    ``sys.modules``. Importing voluptuous first yields the real package, every
    ``isinstance`` check against ``vol.Schema`` fails, and every platform
    reports zero keys with no error. Hence ``import homeassistant`` happens
    before anything else touches voluptuous.

  * Unit constants that moved keep living as ``_DEPRECATED_*`` objects
    (``PERCENTAGE`` is now ``UnitOfRatio.PERCENTAGE.value``; ``ppm``/``ppb``
    moved into ``UnitOfRatio``). Walking module globals without skipping those
    names bakes dead constants into the catalog.
"""

from __future__ import annotations

# ruff: noqa: E402 -- the homeassistant import MUST precede voluptuous; see above.
import homeassistant  # isort: skip  (installs probatio as voluptuous)

import argparse
import enum
import inspect
import json
import subprocess
import sys
from pathlib import Path
from typing import Any

import voluptuous as vol  # this is probatio's shim, thanks to the import above

# --------------------------------------------------------------------------
# helpers
# --------------------------------------------------------------------------

UNDEFINED = object()


def _enum_values(cls: type[enum.Enum]) -> list[str]:
    """Member values of a StrEnum, in declaration order.

    Imported, never AST-parsed: HA uses `EnumWithDeprecatedMembers` metaclasses
    and `set(EnumClass)` expressions that no reasonable parser survives.
    """
    return [m.value for m in cls]


def _flag_values(cls: type[enum.IntFlag]) -> dict[str, int]:
    return {m.name: int(m.value) for m in cls}


def _sorted_strs(values: Any) -> list[str]:
    out: set[str] = set()
    for v in values:
        if v is None:
            continue
        out.add(v.value if isinstance(v, enum.Enum) else str(v))
    return sorted(out)


def _key_name(marker: Any) -> str:
    return str(marker)


def _marker_default(marker: Any) -> Any:
    """Resolve a voluptuous/probatio marker default.

    ``Marker.default`` is a factory, and its "no default" sentinel is a class,
    not ``None`` — calling it blindly turns "unset" into a stray object.
    """
    default = getattr(marker, "default", UNDEFINED)
    if default is UNDEFINED or default is None:
        return None
    if callable(default):
        try:
            value = default()
        except Exception:  # noqa: BLE001 - a defaulted factory may need a hass
            return None
    else:
        value = default
    if value is None or value.__class__.__name__ == "Undefined":
        return None
    if isinstance(value, enum.Enum):
        return value.value
    if isinstance(value, (str, int, float, bool)):
        return value
    if isinstance(value, (list, tuple, set)):
        return _sorted_strs(value)
    return None


def _allowed_values(validator: Any) -> list[str] | None:
    """Extract the value whitelist of a `vol.In(...)`, if the validator is one."""
    container = getattr(validator, "container", None)
    if container is None and hasattr(validator, "validators"):
        for sub in validator.validators:
            found = _allowed_values(sub)
            if found:
                return found
        return None
    if container is None:
        return None
    try:
        return _sorted_strs(container)
    except TypeError:
        return None


# _TYPE_BY_NAME maps a Home Assistant validator's name to the JSON type it
# accepts. It exists because JSON cannot tell 1 from 1.0: a consumer generating
# typed structs from this catalog would otherwise have to guess whether
# `min_temp` is an integer or a float, and guessing wrong is how a discovery
# payload ends up with a key Home Assistant rejects.
_TYPE_BY_NAME = {
    "boolean": "bool",
    "positive_int": "int",
    "positive_float": "float",
    "ensure_list": "list",
    "ensure_list_csv": "list",
    "string": "str",
    "template": "str",
    "valid_subscribe_topic": "str",
    "valid_publish_topic": "str",
    "icon": "str",
    "url": "str",
}

_TYPE_BY_PY = {bool: "bool", int: "int", float: "float", str: "str"}


def _value_type(validator: Any, depth: int = 0) -> str | None:
    """Name the JSON type a validator accepts: bool, int, float, list or str.

    Returns None when the validator says nothing useful (a bare `vol.Range`,
    a whitelist, a custom callable) — the consumer then falls back to its own
    default rather than being told something wrong.
    """
    if depth > 6:
        return None
    if isinstance(validator, (list, tuple, set)):
        return "list"
    # vol.Coerce carries the target type; a bare builtin is used directly.
    coerced = getattr(validator, "type", None)
    for candidate in (coerced, validator):
        # `in` on the lookup table hashes the candidate, and a schema object is
        # unhashable — compare identity against the four builtins instead.
        for py_type, name in _TYPE_BY_PY.items():
            if candidate is py_type:
                return name
    name = getattr(validator, "__name__", None) or type(validator).__name__
    if name in _TYPE_BY_NAME:
        return _TYPE_BY_NAME[name]
    # vol.All / vol.Any wrap the validator that actually names the type; the
    # first that does wins, which is why ensure_list must precede [cv.string].
    for sub in getattr(validator, "validators", ()) or ():
        found = _value_type(sub, depth + 1)
        if found:
            return found
    # A hand-written validator function names its type in its return
    # annotation, which is how `valid_qos_schema` is known to yield an int.
    annotated = getattr(validator, "__annotations__", {}).get("return")
    for py_type, name in _TYPE_BY_PY.items():
        if annotated is py_type:
            return name
    if getattr(annotated, "__origin__", None) in (list, set, tuple):
        return "list"
    return None


def collect_schema(schema: Any, out: dict[str, dict[str, Any]]) -> None:
    """Walk a (probatio) schema recursively, collecting its top-level keys.

    Handles the `.extend()` chains and the `vol.All(schema, validate_fn)`
    wrappers every MQTT platform uses.
    """
    if isinstance(schema, vol.Schema):
        inner = getattr(schema, "schema", None)
        if isinstance(inner, dict):
            for marker, validator in inner.items():
                name = _key_name(marker)
                entry = {
                    "required": type(marker).__name__ == "Required",
                    "default": _marker_default(marker),
                }
                allowed = _allowed_values(validator)
                if allowed:
                    entry["allowed"] = allowed
                value_type = _value_type(validator)
                if value_type:
                    entry["type"] = value_type
                out[name] = entry
        else:
            collect_schema(inner, out)
        return
    for sub in getattr(schema, "validators", ()) or ():
        collect_schema(sub, out)


# --------------------------------------------------------------------------
# Stage 0 — read HA's own generated artifacts
# --------------------------------------------------------------------------


def stage0(core: Path) -> dict[str, Any]:
    gen = core / "homeassistant" / "generated"
    out: dict[str, Any] = {}

    out["device_classes"] = json.loads((gen / "device_classes.json").read_text())
    out["sensor"] = json.loads((gen / "sensor.json").read_text())

    icons: dict[str, Any] = {}
    components = core / "homeassistant" / "components"
    for path in sorted(components.glob("*/icons.json")):
        try:
            payload = json.loads(path.read_text())
        except json.JSONDecodeError:
            continue
        component = payload.get("entity_component")
        if component:
            icons[path.parent.name] = component
    out["icons"] = icons
    return out


# --------------------------------------------------------------------------
# Stage 1 — import and introspect
# --------------------------------------------------------------------------

# (json key, module path, symbol) for the plain StrEnum vocabularies.
ENUM_TARGETS: list[tuple[str, str, str]] = [
    ("sensor_device_class", "homeassistant.components.sensor.const", "SensorDeviceClass"),
    ("sensor_state_class", "homeassistant.components.sensor.const", "SensorStateClass"),
    ("binary_sensor_device_class", "homeassistant.components.binary_sensor", "BinarySensorDeviceClass"),
    ("number_device_class", "homeassistant.components.number.const", "NumberDeviceClass"),
    ("number_mode", "homeassistant.components.number.const", "NumberMode"),
    ("cover_device_class", "homeassistant.components.cover.const", "CoverDeviceClass"),
    ("switch_device_class", "homeassistant.components.switch", "SwitchDeviceClass"),
    ("button_device_class", "homeassistant.components.button", "ButtonDeviceClass"),
    ("event_device_class", "homeassistant.components.event", "EventDeviceClass"),
    ("update_device_class", "homeassistant.components.update", "UpdateDeviceClass"),
    ("humidifier_device_class", "homeassistant.components.humidifier", "HumidifierDeviceClass"),
    ("valve_device_class", "homeassistant.components.valve.const", "ValveDeviceClass"),
    ("media_player_device_class", "homeassistant.components.media_player", "MediaPlayerDeviceClass"),
    ("text_mode", "homeassistant.components.text", "TextMode"),
    ("device_tracker_source_type", "homeassistant.components.device_tracker.const", "SourceType"),
    ("entity_category", "homeassistant.const", "EntityCategory"),
    ("hvac_mode", "homeassistant.components.climate.const", "HVACMode"),
    ("hvac_action", "homeassistant.components.climate.const", "HVACAction"),
    ("color_mode", "homeassistant.components.light.const", "ColorMode"),
    ("cover_state", "homeassistant.components.cover.const", "CoverState"),
    ("valve_state", "homeassistant.components.valve.const", "ValveState"),
    ("lock_state", "homeassistant.components.lock", "LockState"),
    ("alarm_state", "homeassistant.components.alarm_control_panel.const", "AlarmControlPanelState"),
    ("vacuum_activity", "homeassistant.components.vacuum.const", "VacuumActivity"),
    ("lawn_mower_activity", "homeassistant.components.lawn_mower.const", "LawnMowerActivity"),
    ("humidifier_action", "homeassistant.components.humidifier", "HumidifierAction"),
    ("media_player_state", "homeassistant.components.media_player.const", "MediaPlayerState"),
]

FEATURE_TARGETS: list[tuple[str, str, str]] = [
    ("climate", "homeassistant.components.climate.const", "ClimateEntityFeature"),
    ("cover", "homeassistant.components.cover.const", "CoverEntityFeature"),
    ("light", "homeassistant.components.light.const", "LightEntityFeature"),
    ("fan", "homeassistant.components.fan", "FanEntityFeature"),
    ("humidifier", "homeassistant.components.humidifier.const", "HumidifierEntityFeature"),
    ("lock", "homeassistant.components.lock", "LockEntityFeature"),
    ("siren", "homeassistant.components.siren.const", "SirenEntityFeature"),
    ("valve", "homeassistant.components.valve.const", "ValveEntityFeature"),
    ("vacuum", "homeassistant.components.vacuum.const", "VacuumEntityFeature"),
    ("water_heater", "homeassistant.components.water_heater", "WaterHeaterEntityFeature"),
    ("alarm_control_panel", "homeassistant.components.alarm_control_panel.const", "AlarmControlPanelEntityFeature"),
    ("update", "homeassistant.components.update.const", "UpdateEntityFeature"),
    ("lawn_mower", "homeassistant.components.lawn_mower.const", "LawnMowerEntityFeature"),
    ("notify", "homeassistant.components.notify", "NotifyEntityFeature"),
    ("media_player", "homeassistant.components.media_player.const", "MediaPlayerEntityFeature"),
    ("camera", "homeassistant.components.camera", "CameraEntityFeature"),
]


def _import(path: str) -> Any:
    __import__(path)
    return sys.modules[path]


def _resolve(spec: tuple[str, str, str]) -> tuple[str, Any] | None:
    key, module_path, symbol = spec
    try:
        module = _import(module_path)
    except Exception as err:  # noqa: BLE001 - report and continue
        print(f"  ! {key}: cannot import {module_path}: {err}", file=sys.stderr)
        return None
    obj = getattr(module, symbol, None)
    if obj is None:
        print(f"  ! {key}: {module_path}.{symbol} is gone", file=sys.stderr)
        return None
    return key, obj


def extract_enums() -> dict[str, list[str]]:
    out: dict[str, list[str]] = {}
    for spec in ENUM_TARGETS:
        resolved = _resolve(spec)
        if resolved:
            out[resolved[0]] = _enum_values(resolved[1])
    return out


def extract_features() -> dict[str, dict[str, int]]:
    out: dict[str, dict[str, int]] = {}
    for spec in FEATURE_TARGETS:
        resolved = _resolve(spec)
        if resolved:
            out[resolved[0]] = _flag_values(resolved[1])
    return out


def extract_units() -> dict[str, Any]:
    """Every UnitOf* StrEnum, plus the converter unit sets.

    Names beginning with ``_DEPRECATED_`` are skipped: they are placeholder
    objects for constants that moved (PERCENTAGE, ppm, ppb), not units.
    """
    const = _import("homeassistant.const")
    units: dict[str, list[str]] = {}
    for name in dir(const):
        if name.startswith("_DEPRECATED_") or not name.startswith("UnitOf"):
            continue
        obj = getattr(const, name)
        if inspect.isclass(obj) and issubclass(obj, enum.Enum):
            units[name] = _enum_values(obj)

    converters: dict[str, Any] = {}
    try:
        uc = _import("homeassistant.util.unit_conversion")
        base = uc.BaseUnitConverter
        for name in dir(uc):
            obj = getattr(uc, name)
            if not (inspect.isclass(obj) and issubclass(obj, base) and obj is not base):
                continue
            unit_class = getattr(obj, "UNIT_CLASS", None)
            valid = getattr(obj, "VALID_UNITS", None)
            if unit_class and valid:
                converters[unit_class] = _sorted_strs(valid)
    except Exception as err:  # noqa: BLE001
        print(f"  ! unit converters unavailable: {err}", file=sys.stderr)

    return {"enums": units, "converters": converters}


def extract_relations() -> dict[str, Any]:
    out: dict[str, Any] = {}
    sensor_const = _import("homeassistant.components.sensor.const")

    dcsc = getattr(sensor_const, "DEVICE_CLASS_STATE_CLASSES", {})
    out["sensor_device_class_state_classes"] = {
        k.value: _sorted_strs(v) for k, v in dcsc.items()
    }

    precision = getattr(sensor_const, "UNITS_PRECISION", {})
    out["sensor_units_precision"] = {
        k.value: {"unit": (u.value if isinstance(u, enum.Enum) else u), "precision": p}
        for k, (u, p) in precision.items()
    }
    out["sensor_default_precision_limit"] = getattr(sensor_const, "DEFAULT_PRECISION_LIMIT", None)

    # AMBIGUOUS_UNITS is how Home Assistant reconciles the two Unicode spellings
    # of a micro prefix. sensor/__init__.py's
    # _native_unit_of_measurement_compat looks the unit up with
    # `AMBIGUOUS_UNITS.get(unit, unit)`, so the legacy U+00B5 spelling is
    # accepted and rewritten to U+03BC rather than rejected. A bridge should
    # publish the canonical spelling — it is what Home Assistant stores — but
    # the old one works, so a consumer reading this table must report a
    # mismatch as an advisory, not as a reason to withhold the entity.
    out["ambiguous_units"] = dict(getattr(sensor_const, "AMBIGUOUS_UNITS", {}))

    try:
        number_const = _import("homeassistant.components.number.const")
        out["number_device_class_units"] = {
            k.value: _sorted_strs(v)
            for k, v in getattr(number_const, "DEVICE_CLASS_UNITS", {}).items()
        }
    except Exception as err:  # noqa: BLE001
        print(f"  ! number DEVICE_CLASS_UNITS unavailable: {err}", file=sys.stderr)

    return out


def _platform_variants(component: str, module: Any) -> tuple[dict[str, Any], str | None]:
    """Sub-schemas of the two platforms that dispatch instead of validating.

    ``light`` picks one of three schemas from the payload's ``schema:`` key, and
    its sub-schemas live in sibling modules rather than on the package.
    ``infrared`` picks by ``device_class`` through a plain dict.
    """
    variants: dict[str, Any] = {}

    mapping = getattr(module, "DISCOVERY_SCHEMA_MAPPING", None)
    if isinstance(mapping, dict) and mapping:
        for variant, sub in mapping.items():
            keys: dict[str, Any] = {}
            collect_schema(sub, keys)
            if keys:
                variants[str(variant)] = {"keys": keys}
        if variants:
            return variants, "device_class"

    if component == "light":
        for variant, sub_module, symbol in (
            ("basic", "schema_basic", "DISCOVERY_SCHEMA_BASIC"),
            ("json", "schema_json", "DISCOVERY_SCHEMA_JSON"),
            ("template", "schema_template", "DISCOVERY_SCHEMA_TEMPLATE"),
        ):
            schema = getattr(module, symbol, None)
            if schema is None:
                try:
                    schema = getattr(
                        _import(f"homeassistant.components.mqtt.light.{sub_module}"),
                        symbol,
                        None,
                    )
                except Exception as err:  # noqa: BLE001
                    print(f"  ! mqtt.light.{sub_module}: {err}", file=sys.stderr)
                    continue
            if schema is None:
                continue
            keys = {}
            collect_schema(schema, keys)
            if keys:
                variants[variant] = {"keys": keys}
        if variants:
            return variants, "schema"

    return {}, None


def extract_mqtt() -> dict[str, Any]:
    """The MQTT integration: abbreviations, per-platform schemas, device bundle."""
    abbrev = _import("homeassistant.components.mqtt.abbreviations")
    mqtt_const = _import("homeassistant.components.mqtt.const")
    schemas_mod = _import("homeassistant.components.mqtt.schemas")

    out: dict[str, Any] = {
        "abbreviations": dict(abbrev.ABBREVIATIONS),
        "device_abbreviations": dict(abbrev.DEVICE_ABBREVIATIONS),
        "origin_abbreviations": dict(abbrev.ORIGIN_ABBREVIATIONS),
        "supported_components": list(mqtt_const.SUPPORTED_COMPONENTS),
    }

    platforms: dict[str, Any] = {}
    for component in mqtt_const.SUPPORTED_COMPONENTS:
        module_path = f"homeassistant.components.mqtt.{component}"
        try:
            module = _import(module_path)
        except Exception as err:  # noqa: BLE001
            print(f"  ! mqtt.{component}: {err}", file=sys.stderr)
            continue

        # Variants are checked BEFORE DISCOVERY_SCHEMA: light and infrared carry
        # both, and their DISCOVERY_SCHEMA is only a meta-schema that validates
        # the discriminator (`schema:` / `device_class`) before dispatching. Read
        # in the wrong order it yields a single key and looks like a platform
        # with almost no options.
        variants, discriminator = _platform_variants(component, module)
        if variants:
            platforms[component] = {"discriminator": discriminator, "variants": variants}
            continue

        schema = getattr(module, "DISCOVERY_SCHEMA", None)
        if schema is not None:
            keys: dict[str, Any] = {}
            collect_schema(schema, keys)
            platforms[component] = {"keys": keys}
            continue

        print(f"  ! mqtt.{component}: no DISCOVERY_SCHEMA found", file=sys.stderr)

    out["platforms"] = platforms

    device_schema: dict[str, Any] = {}
    collect_schema(getattr(schemas_mod, "DEVICE_DISCOVERY_SCHEMA", None), device_schema)
    out["device_discovery_schema"] = device_schema

    entity_common: dict[str, Any] = {}
    collect_schema(getattr(schemas_mod, "MQTT_ENTITY_COMMON_SCHEMA", None), entity_common)
    out["entity_common_schema"] = entity_common

    device_info: dict[str, Any] = {}
    collect_schema(getattr(schemas_mod, "MQTT_ENTITY_DEVICE_INFO_SCHEMA", None), device_info)
    out["device_info_schema"] = device_info

    origin_info: dict[str, Any] = {}
    collect_schema(getattr(schemas_mod, "MQTT_ORIGIN_INFO_SCHEMA", None), origin_info)
    out["origin_info_schema"] = origin_info

    # The two places where MQTT carries feature names as strings rather than
    # inferring them from topic presence.
    out["feature_strings"] = {}
    alarm_features = getattr(mqtt_const, "ALARM_CONTROL_PANEL_SUPPORTED_FEATURES", None)
    if alarm_features:
        out["feature_strings"]["alarm_control_panel"] = sorted(alarm_features)
    try:
        vacuum = _import("homeassistant.components.mqtt.vacuum")
        s2s = getattr(vacuum, "SERVICE_TO_STRING", {})
        out["feature_strings"]["vacuum"] = sorted(s2s.values())
        default = getattr(vacuum, "DEFAULT_SERVICE_STRINGS", None)
        if default:
            out["feature_strings"]["vacuum_default"] = sorted(default)
    except Exception as err:  # noqa: BLE001
        print(f"  ! mqtt vacuum feature strings: {err}", file=sys.stderr)

    return out


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------


def ha_version(core: Path) -> dict[str, str]:
    const = _import("homeassistant.const")
    version = f"{const.MAJOR_VERSION}.{const.MINOR_VERSION}.{const.PATCH_VERSION}"
    try:
        ref = subprocess.run(
            ["git", "describe", "--tags"],
            cwd=core,
            capture_output=True,
            text=True,
            check=False,
        ).stdout.strip()
    except OSError:
        ref = ""
    return {"version": version, "ref": ref or version}


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True, ensure_ascii=False) + "\n")
    print(f"  wrote {path.name} ({path.stat().st_size:,} bytes)")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--core", required=True, type=Path, help="home-assistant/core checkout")
    parser.add_argument("--out", required=True, type=Path, help="data/ directory to write")
    args = parser.parse_args()

    core: Path = args.core.resolve()
    out: Path = args.out.resolve()
    if not (core / "homeassistant" / "const.py").is_file():
        print(f"error: {core} is not a home-assistant/core checkout", file=sys.stderr)
        return 2

    meta = ha_version(core)
    print(f"Home Assistant {meta['version']} ({meta['ref']})")

    print("Stage 0 — HA's own generated artifacts")
    s0 = stage0(core)
    write_json(out / "device_classes.json", s0["device_classes"])
    write_json(out / "sensor.json", s0["sensor"])
    write_json(out / "icons.json", s0["icons"])

    print("Stage 1 — import and introspect")
    write_json(out / "enums.json", extract_enums())
    write_json(out / "features.json", extract_features())
    write_json(out / "units.json", extract_units())
    write_json(out / "relations.json", extract_relations())
    write_json(out / "mqtt.json", extract_mqtt())
    write_json(out / "snapshot.json", meta)

    print("done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
