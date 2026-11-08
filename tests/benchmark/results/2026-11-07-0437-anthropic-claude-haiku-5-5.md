# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. A full run after the schema rewrite but before the two prompt rules its three silently wrong audiences led to. The current report is [2026-11-09-0220-anthropic-claude-haiku-5-5.md](2026-11-09-0220-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-07 04:37, 1 runs of 246 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|237|3|0|0|4|1|0|1|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|55|0|0|0|2|0|0|0|0|0|
|tuning|182|3|0|0|2|1|0|1|0|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|7|0|0|0|0|1|0|0|0|0|
|behavior|21|0|0|0|0|0|0|0|0|0|
|city_forms|16|0|0|0|0|0|0|0|0|0|
|combined|8|1|0|0|0|0|0|0|0|0|
|content|17|0|0|0|0|0|0|1|0|0|
|injection|18|0|0|0|0|0|0|0|0|0|
|language|7|1|0|0|0|0|0|0|0|0|
|many_cities|13|0|0|0|0|0|0|0|0|0|
|no_audience|10|0|0|0|0|0|0|0|0|0|
|noise|23|0|0|0|3|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|21|0|0|0|1|0|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|22|0|0|0|0|0|0|0|0|0|
|unsupported|19|1|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `content-02` It mentions Kürze, which is not in the brief.; It mentions Kürze, which is not in the brief.
- `content-03` It mentions Ihren, which is not in the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-14` purchased_within_days is 30 but the brief does not say that; use the time it gives, or record an assumption if it gives none

6 runs needed a repair, 240 did not.

## Everything that was not ok

- **silent_wrong** `combined-02` "Son 14 günde sepete ekleyip 30 gündür alışveriş yapmayan Ankara müşterileri" 
  - {"brief":"Son 14 günde sepete ekleyip 30 gündür alışveriş yapmayan Ankara müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","cart_added_within_days":14,"no_purchase_since_cart":true,"not_purchased_within_days":30},"unmatched":null,"unsupported":[],"unsure":null}
- **silent_wrong** `language-07` "Almanca konuşan Ankara kullanıcıları" 
  - {"brief":"Almanca konuşan Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","language":"de"},"unmatched":null,"unsupported":[],"unsure":null}
- **silent_wrong** `unsupported-15` "Ayakkabı alan Ankara müşterileri" 
  - {"brief":"Ayakkabı alan Ankara müşterileri","not_applied":null,"not_audience":["Ayakkabı"],"rule":{"city":"Ankara"},"unmatched":null,"unsupported":[],"unsure":null}
- **false_block** `noise-21` "Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine" 
  - {"brief":"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["cart activity"],"unsupported":[],"unsure":null}
- **false_block** `noise-24` "Tüm Ankara müşterilerine, yaş fark etmeksizin" 
  - {"brief":"Tüm Ankara müşterilerine, yaş fark etmeksizin","not_applied":null,"not_audience":null,"rule":{"city":"Ankara"},"unmatched":null,"unsupported":["yaş fark etmeksizin"],"unsure":null}
- **false_block** `noise-25` "Erkek kadın fark etmeksizin tüm Bursa müşterileri" 
  - {"brief":"Erkek kadın fark etmeksizin tüm Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["gender"],"unsupported":[],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri, son 7 günde mesaj almamış olanlar da","not_applied":["„son 7 günde mesaj almamış olanlar da” ifadesi, mevcut filtrelerle VE mantığında birleştirildiği için yalnızca kitleyi daraltır; bu gruptan ayrı bir kitle olarak eklenemez. Bu nedenle segment değiştirilmedi."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar da"],"unsure":null}
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
- **rejected** `content-03` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" 
  - "content failed validation: It mentions Ihrem, which is not in the brief.; It mentions Ihrem, which is not in the brief.; It mentions Ihren, which is not in the brief."
