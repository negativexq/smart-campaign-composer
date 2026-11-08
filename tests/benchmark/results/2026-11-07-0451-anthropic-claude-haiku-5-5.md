# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. The last full run before the noise-21, noise-24 and noise-25 fixes and before an SMS carried the brief's link as {{link}}. The current report is [2026-11-09-0220-anthropic-claude-haiku-5-5.md](2026-11-09-0220-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-07 04:51, 1 runs of 247 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|241|0|0|0|5|1|0|0|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|55|0|0|0|2|0|0|0|0|0|
|tuning|186|0|0|0|3|1|0|0|0|0|

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
|noise|23|0|0|0|3|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|21|0|0|0|1|0|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|22|0|0|0|0|0|0|0|0|0|
|unsupported|20|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `content-03` It mentions Ihren, which is not in the brief.
- `content-08` It mentions Kürze, which is not in the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-15` segment must have at least one filter
- `refinement-10` city is "İzmir" but the brief does not name it (it names: Ankara)

7 runs needed a repair, 240 did not.

## Everything that was not ok

- **false_block** `city_forms-14` "Customers based in Adana, new arrivals" 
  - {"brief":"Customers based in Adana, new arrivals","not_applied":null,"not_audience":null,"rule":{"city":"Adana"},"unmatched":null,"unsupported":["new arrivals"],"unsure":null}
- **false_block** `noise-21` "Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine" 
  - {"brief":"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["cart activity"],"unsupported":[],"unsure":null}
- **false_block** `noise-24` "Tüm Ankara müşterilerine, yaş fark etmeksizin" 
  - {"brief":"Tüm Ankara müşterilerine, yaş fark etmeksizin","not_applied":null,"not_audience":null,"rule":{"city":"Ankara"},"unmatched":null,"unsupported":["yaş fark etmeksizin"],"unsure":null}
- **false_block** `noise-25` "Erkek kadın fark etmeksizin tüm Bursa müşterileri" 
  - {"brief":"Erkek kadın fark etmeksizin tüm Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["gender"],"unsupported":[],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri ve son 7 günde mesaj almamış olanlar","not_applied":["The filters combine with AND, so adding customers who received no message in the last 7 days would narrow the audience to people who also meet the 60-day purchase condition instead of adding them. No available filter adds a second group, so this was not applied and the audience is unchanged."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar"],"unsure":null}
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
