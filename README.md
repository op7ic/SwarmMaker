# SwarmMaker

Compile unstructured knowledge into validated, installable AI agent swarms.

## Problem Statement

Translating loose human knowledge (notes, API docs, specs, scratch files) into structured AI agent configurations is manual, error-prone, and non-reproducible. The resulting agent scaffolds drift from source material, contain unstated assumptions, break when switching model providers, and have no evidence trail proving correctness.

SwarmMaker automates this as a **two-stage compiler**:

```
  ┌──────────────────┐       ┌──────────────────────┐       ┌──────────────────────┐
  │                  │       │      STAGE 1          │       │      STAGE 2         │
  │   Loose Docs     │       │    (LLM-backed)       │       │   (deterministic)    │
  │                  │       │                      │       │                      │
  │  - notes.md      │       │  .tasks/ ledger       │       │  .claude/            │
  │  - api_docs.md   │──────>│                      │──────>│  .codex/             │
  │  - specs.md      │       │  - 9 ledger files    │       │  .gemini/            │
  │  - scratch.txt   │       │  - 7 IR artifacts    │       │  README.md           │
  │  - ...           │       │  - evidence.json     │       │  install.sh          │
  │                  │       │  - validation report  │       │                      │
  └──────────────────┘       └──────────────────────┘       └──────────────────────┘
                                       │
                              ┌────────┴────────┐
                              │   Validation    │
                              │                 │
                              │  Programmatic   │
                              │  Pre-screen     │
                              │  Adversarial    │
                              │  Revision (x3)  │
                              │  Parity check   │
                              └─────────────────┘
```

**Stage 1** uses LLM calls to decompose source material into a shared `.tasks/` ledger with per-claim citations. **Stage 2** is a deterministic, LLM-free render from that ledger into platform-specific output trees. The expensive work happens once; rendering to N targets is a transform.

## Architecture

### Pipeline Phases

| Phase | LLM Calls | Input | Output | Failure Mode |
|-------|-----------|-------|--------|--------------|
| 1. Ingest | 0 | Source folder | Evidence manifest, complexity analysis | Missing/unreadable files recorded, not hidden |
| 2. IR Emit | 0 | Ingestion output + routing | 7 JSON artifacts under `.tasks/ir/` | Contract validation rejects malformed schemas |
| 3. Generate | 9 (parallelizable) | Compiled prompts + source | 9 `.tasks/` ledger files | Per-task retry with backoff; min-length enforcement |
| 4. Validate | 1-10 | Generated ledger | Validation report | Multi-round: programmatic → pre-screen → adversarial review → revision (up to 3 rounds) → post-screen |
| 5. Render | 0 | Validated ledger | Platform trees + README + installer | Atomic staged write; parity check across targets |

### Data Flow

```
┌─────────────┐
│Source Folder│
│ (loose docs)│
└──────┬──────┘
       │
       v
┌──────────────┐    ┌───────────────┐    ┌───────────────┐
│  [1] Ingest  │───>│ [2] Discover  │───>│  [3] Route    │
│              │    │               │    │               │
│ Walk files   │    │ Scan PATH for │    │ Assign roles: │
│ Record       │    │ claude, codex,│    │ generator,    │
│ evidence     │    │ gemini CLIs   │    │ critic,       │
│ Analyze      │    │ Probe caps +  │    │ renderer      │
│ complexity   │    │ versions      │    │ Log fallbacks │
└──────┬───────┘    └───────────────┘    └───────┬───────┘
       │                                         │
       v                                         v
┌──────────────┐                        ┌────────────────┐
│  [4] IR Emit │                        │  [5] Swarm     │
│              │                        │                │
│7 versioned   │                        │ 9 tasks run    │
│JSON artifacts│──────────────────────>│ concurrently   │
│              │   (prompts compiled    │ (round-robin   │
│ .tasks/ir/   │    from IR + source)   │  across LLMs)  │
└──────────────┘                        └───────┬────────┘
                                                │
                                                v
                                     ┌─────────────────────┐
                                     │  [6] Validate       │
                                     │                     │
                                     │  Programmatic ──┐   │
                                     │  Pre-screen  ───┤   │
                                     │  Adversarial ───┤   │
                                     │  Revision x3 ───┤   │
                                     │  Post-screen ───┤   │
                                     │  Parity check ──┘   │
                                     └─────────┬───────────┘
                                               │
                                               v
                                     ┌─────────────────────┐
                                     │  [7] Render         │
                                     │                     │
                                     │  .claude/  .codex/  │
                                     │  .gemini/           │
                                     │  README.md          │
                                     │  install.sh         │
                                     └─────────────────────┘
```

### Agent Decomposition Model

Generated agents follow Boyd's **OODA loop** (Observe-Orient-Decide-Act) [1]:

| Role | Responsibility | Example |
|------|---------------|---------|
| **Observe** | Gather and normalize input, preserve evidence | Alert ingestion, file inventory |
| **Orient** | Analyze, correlate, decompose into structure | Alert correlation, dependency mapping |
| **Decide** | Apply rules, validate constraints, choose paths | Priority classification, routing decisions |
| **Act** | Execute workflows, produce outputs | Runbook generation, notification delivery |

Multiple agents may share an OODA role when the domain requires distinct execution concerns within a phase. The agent count is the minimum required to cover all source-backed responsibilities.

[1] Boyd, J. R. (1996). *The Essence of Winning and Losing*. Unpublished briefing.

## Design Invariants

These are not guidelines. They are enforced by the pipeline and tested:

| Invariant | Enforcement |
|-----------|-------------|
| No silent defaults | Missing required facts become `UNKNOWN`; dependent decisions are blocked |
| No hidden fallbacks | Every fallback is counted, recorded in evidence, and visible in the validation report |
| No fabrication | Pre-screen detects fabrication patterns; adversarial review checks source fidelity |
| No partial output | Atomic staged writes; incomplete runs leave no artifacts in the output directory |
| No untracked provider routing | Routing decisions are persisted as machine-readable JSON with fallback accounting |
| No success without evidence | Validation report is mandatory on both success and failure paths |
| No cross-target drift | Parity validation checks skill/agent/metadata consistency across all selected platforms |
| No stale citations | Programmatic link checker + template leak detector run before and after revision |

## Performance Characteristics

Measured on a real 4-file input (10KB source material) producing a codex skill bundle:

| Metric | Claude CLI | Codex CLI | Mixed (Claude gen + Codex critic) |
|--------|-----------|-----------|-----------------------------------|
| Generation (9 tasks) | ~16 min (serial) | ~45 min (serial) | ~7 min (concurrent) |
| Adversarial review | ~1.5 min | ~5 min | N/A (uses critic) |
| Revision (per file) | ~2 min | ~4 min | N/A (uses generator) |
| Total (with revision) | ~35 min | ~70 min | ~25 min |
| Output size | ~163 KB | ~386 KB | Varies by generator |

Scaling: generation time is O(N) in task count (currently fixed at 9). Prompt size is O(S) in source material size. Revision rounds are bounded at 3 with regression detection.

## Comparison With Alternatives

| Approach | SwarmMaker | LangChain/CrewAI/AutoGen | Manual prompt engineering |
|----------|-----------|--------------------------|--------------------------|
| When it runs | Build time (offline) | Runtime (online) | Human time |
| Output | Static reviewable files | Running processes | Prompts in code |
| Provider lock-in | None (renders to any target) | Framework-specific | Provider-specific |
| Validation | Adversarial + programmatic | Unit tests on chains | Manual review |
| Evidence trail | Full (evidence.json, IR, report) | Logs | None |
| Reproducibility | Same input → same structure | Non-deterministic | Depends on author |
| Source fidelity | Per-claim citations required | No citation contract | No citation contract |

SwarmMaker does not replace runtime agent frameworks. It produces the **knowledge artifacts** those frameworks consume.

## Known Limitations

1. **LLM output is non-deterministic.** Two runs with the same input produce structurally similar but textually different ledgers. The validation pipeline catches drift but cannot guarantee identical output.
2. **No incremental regeneration.** Changing one source file regenerates all 9 ledger files. Delta-based regeneration is not implemented.
3. **Single revision target per round.** The adversarial reviewer sees all files but revision is per-file, not holistic. Cross-file issues may require multiple rounds.
4. **No runtime validation.** SwarmMaker validates the generated artifacts, not whether the installed skill bundle actually works when invoked by an agent at runtime.
5. **Tool synthesis is planning-only.** The tool synthesis module decides whether tools are needed and what language they should use, but does not generate executable code.
6. **Citation density heuristic.** The pre-screen uses a sub-linear formula for expected citation count. Very long documents (>50K chars) may trigger false positives.

## Installation

### From source

```bash
make build
# Binary at ./build/swarm-me
```

Install to `~/.local/bin`:

```bash
make install
```

### Prerequisites

At least one LLM CLI:

- [claude](https://docs.anthropic.com/en/docs/claude-cli) (Anthropic)
- [codex](https://github.com/openai/codex) (OpenAI)
- [gemini](https://ai.google.dev/gemini-api/docs/cli) (Google)

Check availability: `swarm-me discover`

## Usage

```
swarm-me --input <dir> --model <provider> --output-swarm <format> [flags]
```

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--input <dir>` | Yes | Source documentation folder |
| `--model <provider>` | Yes | Generator LLM: `codex`, `claude`, or `gemini` |
| `--output-swarm <format>` | Yes | Target(s): `claude`, `codex`, `gemini`, `all`, or comma-separated |
| `-o, --output-folder <dir>` | No | Output folder (default: `.`) |
| `--critique <provider>` | No | Critic LLM (auto-detected if omitted) |
| `-n, --name <name>` | No | Project name (derived from folder if omitted) |
| `--model-primary <model>` | No | Specific model override for generator |
| `--model-critic <model>` | No | Specific model override for critic |
| `--prompt-pack <path>` | No | Custom prompt pack JSON |
| `--dry-run` | No | Preview without LLM calls |
| `--force` | No | Overwrite existing output |
| `-v, --verbose` | No | Show full LLM interactions |

### Examples

```bash
# Claude generates, codex reviews, output as codex skill bundle
swarm-me --input ./notes --model claude --critique codex --output-swarm codex -o ./SKILL

# All platforms from one run
swarm-me --input ./notes --model claude --output-swarm all -o ./SKILL

# Custom prompt pack
swarm-me prompt-pack export -o ./pack.json   # export, edit, then:
swarm-me --input ./notes --model claude --output-swarm codex --prompt-pack ./pack.json -o ./SKILL
```

## Output Structure

```
<output>/
  .tasks/                          # Stage 1: shared build ledger
    context.md                     # Source context with per-claim citations
    tasks.md                       # Task decomposition from source goals
    skills.md                      # Skill definitions (renderer input)
    agents.md                      # Agent definitions (renderer input)
    todo.md                        # Delivery queue with OODA phases
    prompts/{product,technical,    # Domain-specific compiled prompts
             tools,deployment}.md
    ir/                            # 7 versioned JSON artifacts
    evidence.json                  # Ingestion + generation evidence
    manifest.json                  # Build manifest with digests
    validation-report.md           # Full PASS/FAIL report
  .codex/                          # Stage 2: platform-specific tree
    AGENTS.md                      # Agent router with OODA roles
    README.md                      # Skill bundle readme
    instructions/                  # Per-skill instruction files
      index.md
      <skill-slug>.md ...
  README.md                        # Bundle readme
  install.sh                       # Installer (--target, --global)
```

## Validation Pipeline

```
┌──────────────────────┐
│  Generated Ledger    │
│  (9 .tasks/ files)   │
└──────────┬───────────┘
           │
           v
┌──────────────────────────────────────────────────────────┐
│  Programmatic Checks (0 LLM calls)                      │
│  - File existence + minimum size                        │
│  - Markdown link integrity                              │
│  - Template leak detection (16 patterns)                │
│  - Meta-commentary filtering                            │
└─────��────┬────────────���───────────────────────────���──────┘
           │
           v
��──────────────────────────────────────────────────────────┐
│  Pre-Screen Gate (0 LLM calls)                          │
��  - Citation density (sub-linear scaling by doc length)   │
│  - Fabrication pattern detection                        │
│  - Boilerplate injection detection                      │
│  - Amplification ratio check                            │
│  - Dimension coverage (deep sources only)               │
│  Thresholds adapt to source depth: shallow/moderate/deep │
└──────────┬───────────────────────────────────────────��───┘
           │
           v
┌─────────────���────────────────────────────────────────────┐
│  Adversarial LLM Review (1 LLM call)                    ��
│  - Cross-file consistency                               │
│  - Source fidelity (fabrication, missing coverage)       │
│  - Citation integrity                                   │
│  - UNKNOWN gate enforcement                             │
│  Verdict: APPROVE or REVISE (with per-file findings)    │
└─────��────┬───────────────────────────────���───────────────┘
           │
           v                    ┌────��─────────────────���
┌────────────���─────────┐   ┌───>��  Round N+1           ���
│  Targeted Revision   │   │    │  (only if flag count │
│  (0-N LLM calls)     │   │    │   decreased; max 3)  │
│                      │   │    └���────────────────────���┘
│  Only flagged files  │───┘
│  are regenerated     │
└──────────┬───────────┘
           │
           v
┌───────────���──────────────────────────────────────────────┐
│  Post-Revision Re-Screen (0 LLM calls)                  ���
│  - Re-runs pre-screen on revised files                  │
│  - Regression detection: stops if flags not decreasing  │
└──────────┬────────────────────────────────────────────��──┘
           │
           v
┌─��───────────────────���──────────────────────────���─────────┐
│  Render Parity Check (0 LLM calls)                      │
│  - Skill coverage matches across all selected platforms  │
│  - Agent roles consistent                               │
│  - Metadata and source references aligned               │
└─────���────┬───────────────────���───────────────────────────���
           │
           v
      PASS / FAIL
```

Concrete pre-screen findings block approval even if the adversarial reviewer returns APPROVE.

## Development

```bash
make build     # Compile to ./build/swarm-me
make test      # All tests with -race
make fmt       # gofmt
make lint      # golangci-lint
make all       # fmt + lint + test + build
make release   # Cross-compile (linux/darwin/windows, amd64/arm64)
```

Source: `src/swarmmaker/`. The root Makefile delegates there.

### Packages

| Package | Role |
|---------|------|
| `cmd/swarm-me` | Entry point, version injection |
| `internal/cli` | 5-phase pipeline orchestrator, validation gates |
| `internal/ingestion` | Evidence-backed source traversal |
| `internal/ir` | Versioned JSON IR with SHA-256 digests |
| `internal/output` | Platform renderers + parity validation |
| `internal/swarm` | Concurrent task engine + pre-screening |
| `internal/executor` | LLM subprocess management (retry, stdin transport, sandbox detection) |
| `internal/discovery` | LLM CLI detection via PATH |
| `internal/routing` | Deterministic routing with fallback accounting |
| `internal/contracts` | Typed schema contracts (12 types) |
| `internal/redaction` | Secret pattern redaction |
| `internal/toolsynthesis` | Tool generation planning |
| `internal/ooda` | OODA state definitions |
| `prompts` | Prompt pack loader, semantic reviewer, compiler |

### Release

[GoReleaser](https://goreleaser.com/) builds for linux/darwin/windows on amd64/arm64. Version injected via `-X main.version={{.Version}}`.

## License

See [LICENSE](LICENSE).
