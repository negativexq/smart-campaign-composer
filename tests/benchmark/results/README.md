# Benchmark results

Each run of `make benchmark` writes a report here (`.md`) and its raw data (`.json`). Only the reports under **Current** describe the system as it is. Every other report is kept as history: it ran on an older prompt, schema, check or case set, or on only some of the cases, and its first line says it is superseded. Read the numbers in the current reports, not the old ones.

## Current

One run of all 247 cases on each model, with the same prompt and cases.

| Model | Report | As expected | Blocked without need | Shown as unsure | Silently wrong | Forbidden text |
|---|---|---|---|---|---|---|
| claude-haiku-5-5 | [2026-11-07-0451](2026-11-07-0451-anthropic-claude-haiku-5-5.md) | 241 | 5 | 1 | 0 | 0 |
| gpt-6-luna | [2026-11-07-0447](2026-11-07-0447-openai-gpt-6-luna.md) | 242 | 5 | 0 | 0 | 0 |

Three of the Claude run's five blocked cases (noise-21, noise-24 and noise-25) were fixed after it; a partial run of those three passed all 9 times, and a regression run of 62 related cases had all 62 as expected (both listed below). One clean run says little about the next one, and these are cases we wrote ourselves, not real traffic. See [DECISIONS.md](../../../DECISIONS.md#the-benchmark-draws-the-limits-it-is-not-a-target).

## History

Each of these is superseded. They are kept because DECISIONS.md cites them as the record of what changed and why.

| Report | Cases | Why it is not current |
|---|---|---|
| [2026-11-08-1744 claude-haiku-5-5](2026-11-08-1744-anthropic-claude-haiku-5-5.md) | 62 | Partial regression run after those fixes: cases with cart, age or gender words, noise, and some city cases |
| [2026-11-08-1534 claude-haiku-5-5](2026-11-08-1534-anthropic-claude-haiku-5-5.md) | 3 × 3 | Partial run to check the noise-21, noise-24 and noise-25 fixes |
| [2026-11-07-0441 gpt-6-luna](2026-11-07-0441-openai-gpt-6-luna.md) | 4 | Partial run to check two prompt rules |
| [2026-11-07-0440 claude-haiku-5-5](2026-11-07-0440-anthropic-claude-haiku-5-5.md) | 4 | Partial run to check two prompt rules |
| [2026-11-07-0437 claude-haiku-5-5](2026-11-07-0437-anthropic-claude-haiku-5-5.md) | 246 | After the schema rewrite, before the two prompt rules its 3 silently wrong audiences led to |
| [2026-11-07-0431 claude-haiku-5-5](2026-11-07-0431-anthropic-claude-haiku-5-5.md) | 30 | Partial run of failed cases to check the schema rewrite |
| [2026-11-07-0424 claude-sonnet-5-5](2026-11-07-0424-anthropic-claude-sonnet-5-5.md) | 246 | One-time diagnostic; Sonnet is not measured by this benchmark |
| [2026-11-07-0417 claude-haiku-5-5](2026-11-07-0417-anthropic-claude-haiku-5-5.md) | 246 | Before the schema rewrite that keeps Claude's filters |
| [2026-11-04-1155 gpt-6-luna](2026-11-04-1155-openai-gpt-6-luna.md) | 246 | Before the prompt and case changes of 2026-11-07 |
| [2026-11-04-0541 gpt-6-luna](2026-11-04-0541-openai-gpt-6-luna.md) | 246 | Before the prompt and case changes of 2026-11-07 |
| [2026-11-04-0509 gpt-6-luna](2026-11-04-0509-openai-gpt-6-luna.md) | 246 | Before the prompt and case changes of 2026-11-07 |
| [2026-11-04-0422 gpt-6-luna](2026-11-04-0422-openai-gpt-6-luna.md) | 246 × 3 | Before the check and prompt changes that followed |
| [2026-11-04-0400 gpt-6-luna](2026-11-04-0400-openai-gpt-6-luna.md) | 224 | First run; three of its four silently wrong audiences came from a check that has since changed |

## Adding a run

A new report starts out neither current nor marked, so `make test` fails until you choose one of these:

- **It is the new result for its model:** list it under Current, move the old one to History, and put the superseded note at the top of the old report.
- **It is a partial or diagnostic run:** put the superseded note at the top of the new report and list it under History.

The test is `TestEveryBenchmarkReportIsCurrentOrMarkedSuperseded` in [results_test.go](../results_test.go).
