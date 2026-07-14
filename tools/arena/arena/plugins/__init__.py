"""Plugin registry. Importing a plugin module self-registers it (see base.register)."""

from . import base  # noqa: F401  (registry surface)
from . import bugfix  # noqa: F401  (self-registers the bugfix job type)
from . import paired_task  # noqa: F401  (self-registers the paired-task job type)
from . import persona_qa  # noqa: F401  (self-registers the persona-qa job type)
from . import swarm  # noqa: F401  (self-registers the swarm job type)
from . import usable_kitsoki_gate  # noqa: F401  (self-registers the usable-kitsoki-gate job type)
