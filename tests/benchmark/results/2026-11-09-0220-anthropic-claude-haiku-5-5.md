# Benchmark: anthropic claude-haiku-5-5

2026-11-09 02:20, 1 runs of 247 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|243|0|0|0|2|2|0|0|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|56|0|0|0|1|0|0|0|0|0|
|tuning|187|0|0|0|1|2|0|0|0|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|7|0|0|0|0|1|0|0|0|0|
|behavior|21|0|0|0|0|0|0|0|0|0|
|city_forms|15|0|0|0|1|0|0|0|0|0|
|combined|9|0|0|0|0|0|0|0|0|0|
|content|18|0|0|0|0|0|0|0|0|0|
|injection|18|0|0|0|0|0|0|0|0|0|
|language|9|0|0|0|0|0|0|0|0|0|
|many_cities|13|0|0|0|0|0|0|0|0|0|
|no_audience|10|0|0|0|0|0|0|0|0|0|
|noise|26|0|0|0|0|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|21|0|0|0|0|1|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|21|0|0|0|1|0|0|0|0|0|
|unsupported|20|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `content-03` It mentions Ihrem, which is not in the brief. It mentions Ihrem, which is not in the brief. It mentions Ruhe, which is not in the brief. It mentions Ihrem, which is not in the brief.
- `content-08` It mentions Kürze, which is not in the brief. It mentions Kürze, which is not in the brief. It mentions Kürze, which is not in the brief.
- `content-09` SMS does not mention the 30% discount from the brief. Email does not mention the 30% discount from the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `refinement-09` purchased_within_days is 14 but the brief does not say that; use the time it gives, or record an assumption if it gives none
- `refinement-10` city is "İzmir" but the brief does not name it (it names: Ankara)

8 runs needed a repair, 239 did not.

## Everything that was not ok

- **false_block** `city_forms-14` "Customers based in Adana, new arrivals" 
  - {"brief":"Customers based in Adana, new arrivals","not_applied":null,"not_audience":null,"rule":{"city":"Adana"},"unmatched":null,"unsupported":["new arrivals"],"unsure":null}
- **false_block** `unseen_negation-08` "Son 30 gündür mağazamıza uğramayan Bursa müşterileri" 
  - {"brief":"Son 30 gündür mağazamıza uğramayan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":null,"unsupported":["Son 30 gündür mağazamıza uğramayan"],"unsure":null}
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
- **visible_miss** `refinement-05` "Ankara customers who bought in the last 60 days" + "add people in İzmir too"
  - {"brief":"Ankara customers who bought in the last 60 days","not_applied":["Adding people in İzmir was not applied: the city filter holds one city, so the audience stays Ankara only."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":null,"unsupported":[],"unsure":null}
