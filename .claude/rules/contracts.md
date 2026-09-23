---
paths:
  - "pkg/model/**"
  - "docs/reference/contracts.md"
  - "wearos-app/app/src/main/java/com/gabriel/agentwatch/model/**"
  - "watchos-app/AgentWatch/Models/**"
---

# Contract files: four copies, one truth

Every JSON shape exists in four places, and they must match exactly:

1. `docs/reference/contracts.md` (the source of truth; change it first)
2. `pkg/model/*.go`
3. `wearos-app/.../model/*.kt`
4. `watchos-app/AgentWatch/Models/*.swift`

- **Follow `.claude/skills/schema-sync/SKILL.md`** for any field change. A Stop hook reminds you if `pkg/model` changed without the others.
- **Additive changes only while clients are deployed:** add optional fields, never rename or remove. Decoders ignore unknown fields.
- **Status values are herdr's enum, verbatim** (`idle / working / blocked / done / unknown`). Never add or remap values.
- **Update the golden file `pkg/model/testdata/agent_state.json`** together with the example in `contracts.md` §1.2. Both clients' parity tests read it.
