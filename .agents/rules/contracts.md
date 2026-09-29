---
trigger: always_on
description: "The four copies of the JSON contracts must stay identical"
---

# Contract files: one truth, several copies

> Applies to: pkg/model, docs/reference/contracts.md, the Wear OS and watchOS models.

Every JSON shape the watch and the bridge exchange has one source of truth, `docs/reference/contracts.md`, and copies in Go, Kotlin and Swift that must match it.

- **Any field change follows `.agents/skills/schema-sync/SKILL.md`:** which copies exist, the rules (additive only, closed enums), the golden file and the steps.
- The git pre-commit hook rejects a `pkg/model` change without the other copies (agy's Stop hook also reminds you).
