---
trigger: always_on
description: "The four copies of the JSON contracts must stay identical"
---

# Contract files: four copies, one truth

> Applies to: pkg/model, docs/reference/contracts.md, the Wear OS and watchOS models.

Every JSON shape exists in four places, and they must match exactly:

1. `docs/reference/contracts.md` (the source of truth; change it first)
2. `pkg/model/*.go`
3. `wearos-app/.../model/*.kt`
4. `watchos-app/AgentWatch/Models/*.swift` (until Phase 6 the legacy client: only `CancelRequest` is mirrored; `docs/STATUS.md`)

- **Follow `.agents/skills/schema-sync/SKILL.md`** for any field change. The git pre-commit hook rejects a `pkg/model` change without the others (agy's Stop hook also reminds you).
- **Additive changes only while clients are deployed:** add optional fields, never rename or remove. Decoders ignore unknown fields.
- **Status values are herdr's enum, verbatim** (`idle / working / blocked / done / unknown`). Never add or remap values.
- **Update the golden file `pkg/model/testdata/agent_state.json`** together with the example in `contracts.md` §1.2. The Wear OS parity tests read it (watchOS will from Phase 6).
