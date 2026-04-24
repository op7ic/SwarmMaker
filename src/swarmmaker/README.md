# AI Swarm Maker

AI Swarm Maker turns loose documentation into agent-swarm scaffolding using installed frontier LLM CLIs. This root `src/swarmmaker` tree is the production workspace; `.idea/planner` is reference material only.

## Status

The production bundle contract now lives in `src/swarmmaker`. `swarm-me` treats `.tasks/` as the central build ledger, persists detailed IR under `.tasks/ir/`, can render one or more hidden platform roots from the same ledger, and hard-fails when a rendered `.claude/`, `.codex/`, or `.gemini/` tree drifts from the shared decomposition.

Real-provider throughput still depends on the installed CLI. Same-provider generation runs are serialized automatically so the tool does not start overlapping provider sessions against one bundle workspace and pretend that concurrency is safe when it is not.

## Setup

```bash
cd src/swarmmaker
go mod download
```

At least one provider CLI must be installed for real generation:

- `codex`
- `claude`
- `gemini`

## Run

```bash
cd src/swarmmaker
make build
./build/swarm-me --help
```

```bash
./build/swarm-me --input ./input --model codex --critique gemini --output-swarm gemini --output-folder ./SKILL
./build/swarm-me --input ./input --model codex --critique gemini --output-swarm all --output-folder ./SKILL
```

To customize generation prompts, export the embedded JSON pack, edit it, then pass it back explicitly:

```bash
./build/swarm-me prompt-pack export --output ./prompt-pack.json
./build/swarm-me --input ./input --model codex --critique gemini --output-swarm gemini --output-folder ./SKILL --prompt-pack ./prompt-pack.json
```

The output folder receives only:

- `.tasks/`
- one or more selected hidden platform trees: `.claude/`, `.codex/`, `.gemini/`
- `README.md`
- `install.sh`

`--output-swarm` accepts a single format, `all`, or a comma-separated list such as `claude,codex`.

Within `.tasks/`, the current build writes `evidence.json`, `manifest.json`, `validation-report.md`, the prompt/decomposition/task ledger files, and detailed IR artifacts under `.tasks/ir/`. The final tree is built from that ledger rather than from a separate `.swarmmaker` staging surface.

When `--output-swarm` selects more than one target, `swarm-me` renders all requested hidden roots from the same `.tasks` blueprint and cross-validates them before writing the final bundle. A renderer that drops a skill, agent role, source reference, or metadata contract fails the build instead of silently diverging.

Pre-screen findings such as low citation density or fabrication signals are handled automatically: concrete file flags flow into adversarial review and targeted revision, and unresolved findings still fail the final decision gate.

Custom prompt packs are not trusted blindly. `swarm-me` performs strict JSON-schema parsing plus a deterministic semantic review that rejects packs that remove review contracts, tell the model to ignore source/evidence/validation, hide fallbacks, or always approve outputs. Persisted `prompt-ir.json` redacts common secret patterns from source material while retaining original and redacted hashes plus a redaction report.

## Test

```bash
cd src/swarmmaker
go test ./...
```

Targeted packages added during productionization:

- `internal/contracts`: versioned product, evidence, routing, validation, artifact, and review contracts.
- `internal/ingestion`: evidence-backed ingestion that records skipped, unreadable, binary, hidden, oversized, symlink, mixed-encoding, and summary-budget events.
- `internal/redaction`: deterministic source redaction for persisted IR artifacts.
- `internal/routing`: deterministic provider routing with explicit fallback accounting.
- `internal/ir`: deterministic JSON artifacts for product definition, source IR, provider capabilities, routing decision, output-tree spec, tool-synthesis request, and prompt IR, persisted under `.tasks/ir/`.
- `internal/output`: Claude/Codex/Gemini output registry, concrete OODA agent files, manifest validation, and cross-target parity validation against the shared `.tasks` blueprint.
- `internal/toolsynthesis`: mandatory/forbidden/unknown tool planning with no guessed language.
- `internal/ooda`: executable OODA state transition model.
- `prompts`: strict JSON prompt-pack loader and semantic reviewer plus SwarmMaker prompt compiler driven by `PromptIR`, including target format, model routing, evidence, IR manifest, prompt-pack digest, tool-language, and validation context.

## Development Plan

Use `../../docs/swarmmaker_master_plan.md` as the source of truth. It contains the accepted G0 plan, task IDs 1-240, team ownership, v1 deferrals, keep/delete/migrate map, hard-validation failure classes, reviewer finding schema, and QA fixture matrix.
