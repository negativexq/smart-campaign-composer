# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. A partial run of 30 cases that had failed, to check the schema rewrite. The current report is [2026-11-09-0220-anthropic-claude-haiku-5-5.md](2026-11-09-0220-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-07 04:31, 1 runs of 30 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|24|1|0|0|4|1|0|0|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|5|0|0|0|0|0|0|0|0|0|
|tuning|19|1|0|0|4|1|0|0|0|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|0|0|0|0|0|1|0|0|0|0|
|behavior|9|0|0|0|0|0|0|0|0|0|
|city_forms|1|0|0|0|0|0|0|0|0|0|
|combined|8|0|0|0|0|0|0|0|0|0|
|content|2|0|0|0|0|0|0|0|0|0|
|language|0|1|0|0|0|0|0|0|0|0|
|noise|0|0|0|0|3|0|0|0|0|0|
|refinement|3|0|0|0|1|0|0|0|0|0|
|unseen_negation|1|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model


0 runs needed a repair, 30 did not.

## Everything that was not ok

- **silent_wrong** `language-07` "Almanca konuşan Ankara kullanıcıları" 
  - {"brief":"Almanca konuşan Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","language":"de"},"unmatched":null,"unsupported":[],"unsure":null}
- **false_block** `noise-21` "Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine" 
  - {"brief":"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["cart activity"],"unsupported":[],"unsure":null}
- **false_block** `noise-24` "Tüm Ankara müşterilerine, yaş fark etmeksizin" 
  - {"brief":"Tüm Ankara müşterilerine, yaş fark etmeksizin","not_applied":null,"not_audience":null,"rule":{"city":"Ankara"},"unmatched":null,"unsupported":["yaş fark etmeksizin"],"unsure":null}
- **false_block** `noise-25` "Erkek kadın fark etmeksizin tüm Bursa müşterileri" 
  - {"brief":"Erkek kadın fark etmeksizin tüm Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["gender"],"unsupported":[],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri ve son 7 günde mesaj almamış olanlar da","not_applied":["Son 7 günde mesaj almamış kişiler eklenemedi. Tüm filtreler birlikte (VE) uygulanır ve bu sistem iki grubu birleştiren bir (VEYA) kitle oluşturamaz. Ek filtre eklemek kitleyi daraltırdı, bu yüzden segment değiştirilmedi."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar da"],"unsure":null}
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
