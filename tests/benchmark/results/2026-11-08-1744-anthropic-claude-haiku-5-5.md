# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. A partial regression run of 62 cases: every case with a cart, age or gender word, all noise cases and six city cases, after the noise-21, noise-24 and noise-25 fixes. The current report is [2026-11-09-0220-anthropic-claude-haiku-5-5.md](2026-11-09-0220-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-08 17:44, 1 runs of 62 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|62|0|0|0|0|0|0|0|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|13|0|0|0|0|0|0|0|0|0|
|tuning|49|0|0|0|0|0|0|0|0|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|behavior|8|0|0|0|0|0|0|0|0|0|
|city_forms|6|0|0|0|0|0|0|0|0|0|
|combined|4|0|0|0|0|0|0|0|0|0|
|content|1|0|0|0|0|0|0|0|0|0|
|injection|2|0|0|0|0|0|0|0|0|0|
|many_cities|1|0|0|0|0|0|0|0|0|0|
|noise|26|0|0|0|0|0|0|0|0|0|
|other_places|4|0|0|0|0|0|0|0|0|0|
|refinement|5|0|0|0|0|0|0|0|0|0|
|unsupported|5|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `content-03` It mentions Ihrem, which is not in the brief. It mentions Ihrem, which is not in the brief. It mentions Ruhe, which is not in the brief. It mentions Ruhe, which is not in the brief.

1 runs needed a repair, 61 did not.

## Everything that was not ok

