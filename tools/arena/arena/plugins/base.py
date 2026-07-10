"""Job-type plugin interface + registry.

A job-type plugin is the ONLY thing that knows what a comparison *means*. It maps
a generic `Cell` onto: which container image runs it, what command drives it
inside the container, and how to grade the container's output into a job-agnostic
`CellResult`. bugfix → oracle; persona-qa → the 19-check review gate; onboarding
→ profile/command assertions. Everything else (enumeration, placement, rollup) is
job-type-agnostic.
"""

from __future__ import annotations

from typing import Protocol, runtime_checkable

from ..model import Cell, CellResult


@runtime_checkable
class JobTypePlugin(Protocol):
    """Contract every comparison job type implements."""

    name: str

    def image(self, cell: Cell) -> str:
        """Container image (tag) this cell runs in."""

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        """Argv to run *inside the container*.

        `live=False` is the no-LLM path (e.g. oracle arming); `live=True` spends
        (the real agent drive). Both produce output the plugin's `score` reads.
        """

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        """Grade the container run into a job-type-agnostic CellResult."""


_REGISTRY: dict[str, JobTypePlugin] = {}


def register(plugin: JobTypePlugin) -> JobTypePlugin:
    _REGISTRY[plugin.name] = plugin
    return plugin


def get(job_type: str) -> JobTypePlugin:
    if job_type not in _REGISTRY:
        raise KeyError(f"no arena plugin registered for job_type={job_type!r}; have {sorted(_REGISTRY)}")
    return _REGISTRY[job_type]


def known() -> list[str]:
    return sorted(_REGISTRY)
