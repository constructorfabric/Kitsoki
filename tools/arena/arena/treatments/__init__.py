"""Reusable treatment definitions for arena comparison jobs."""

from .base import DriverResult, DriverServices, TreatmentCatalogEntry, TreatmentDriver
from .capabilities import (
    CODEACT_CAPABILITY_PRESET,
    CODEACT_MCP_SERVER,
    CODEACT_MCP_TOOL,
    DEFAULT_CAPABILITY_PRESETS,
    DEFAULT_CODEACT_AGENT,
    assert_codeact_launch_plan,
    capability_hash,
    capability_preset_json,
    canonical_json,
    merged_capability_presets,
)
from .registry import (
    TREATMENT_DRIVERS,
    canonical_treatment,
    known_treatments,
    resolve_treatment_driver,
    treatment_catalog,
    validate_driver_args,
    validate_driver_errors,
)

__all__ = [
    "CODEACT_CAPABILITY_PRESET",
    "CODEACT_MCP_SERVER",
    "CODEACT_MCP_TOOL",
    "DEFAULT_CAPABILITY_PRESETS",
    "DEFAULT_CODEACT_AGENT",
    "DriverResult",
    "DriverServices",
    "TREATMENT_DRIVERS",
    "TreatmentCatalogEntry",
    "TreatmentDriver",
    "assert_codeact_launch_plan",
    "canonical_json",
    "canonical_treatment",
    "capability_hash",
    "capability_preset_json",
    "known_treatments",
    "merged_capability_presets",
    "resolve_treatment_driver",
    "treatment_catalog",
    "validate_driver_args",
    "validate_driver_errors",
]
