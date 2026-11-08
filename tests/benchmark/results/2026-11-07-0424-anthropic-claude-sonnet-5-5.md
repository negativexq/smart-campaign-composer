# Benchmark: anthropic claude-sonnet-5-5

> **Superseded.** Kept as history, not a current result. A one-time diagnostic before the schema rewrite, to see whether a larger model failed the same way. Sonnet is not measured by this benchmark. The current report is [2026-11-07-0451-anthropic-claude-haiku-5-5.md](2026-11-07-0451-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-07 04:24, 1 runs of 246 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|220|2|0|0|23|0|0|0|1|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|51|0|0|0|6|0|0|0|0|0|
|tuning|169|2|0|0|17|0|0|0|1|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|8|0|0|0|0|0|0|0|0|0|
|behavior|13|1|0|0|7|0|0|0|0|0|
|city_forms|16|0|0|0|0|0|0|0|0|0|
|combined|1|0|0|0|8|0|0|0|0|0|
|content|17|0|0|0|0|0|0|0|1|0|
|injection|18|0|0|0|0|0|0|0|0|0|
|language|7|1|0|0|0|0|0|0|0|0|
|many_cities|13|0|0|0|0|0|0|0|0|0|
|no_audience|10|0|0|0|0|0|0|0|0|0|
|noise|23|0|0|0|3|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|18|0|0|0|4|0|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|21|0|0|0|1|0|0|0|0|0|
|unsupported|20|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `ambiguous-02` segment must have at least one filter
- `behavior-01` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `behavior-02` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `behavior-03` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other
- `behavior-04` no_purchase_since_cart requires cart_added_within_days
- `behavior-05` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `behavior-06` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `behavior-18` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `combined-02` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other
- `combined-03` purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `combined-06` purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `combined-08` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `content-03` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `content-04` It mentions Ruhe, which is not in the brief.; It mentions Ruhe, which is not in the brief.
- `content-05` It mentions Ruhe, which is not in the brief.
- `content-08` It mentions Ihrer, which is not in the brief.
- `content-17` It mentions Ruhe, which is not in the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-14` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `no_audience-09` segment must have at least one filter
- `refinement-10` city is "İzmir" but the brief does not name it (it names: Ankara)
- `refinement-12` segment must have at least one filter
- `refinement-19` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-20` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-21` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-22` purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days

28 runs needed a repair, 218 did not.

## Everything that was not ok

- **silent_wrong** `behavior-21` "Hiç alışveriş yapmamış Ankara kullanıcıları" 
  - {"brief":"Hiç alışveriş yapmamış Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","not_purchased_within_days":365},"unmatched":null,"unsupported":[],"unsure":null}
- **silent_wrong** `language-07` "Almanca konuşan Ankara kullanıcıları" 
  - {"brief":"Almanca konuşan Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","language":"de"},"unmatched":null,"unsupported":[],"unsure":null}
- **false_block** `behavior-01` "Son 30 günde sepete ürün ekleyip almayan Ankara müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-02` "Son 7 günde sepetini terk eden İzmir kullanıcıları" 
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-03` "Ankara users who abandoned their cart last week" 
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-04` "Sepetinde ürün bırakan Bursa müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-05` "İstanbul users who added to cart in the last 14 days" 
  - "rejected: proposal failed validation: purchased_within_days is 1 but the brief does not say that; use the time it gives, or record an assumption if it gives none; not_messaged_within_days is 1 but the brief does not say that; use the time it gives, or record an assumption if it gives none; purchased_within_days is set but the brief does not mention a purchase; language is \"tr\" but the brief does not ask for it"
- **false_block** `behavior-06` "Son 14 günde sepete ürün ekleyen İzmir kullanıcıları" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365"
- **false_block** `behavior-18` "Sepetinde ürün olan Ankara müşterileri, son 30 gün" 
  - "rejected: proposal failed validation: language is \"tr\" but the brief does not ask for it"
- **false_block** `combined-01` "Son 60 günde alışveriş yapmış, son 7 günde mesaj almamış Ankara müşterileri" 
  - {"brief":"Son 60 günde alışveriş yapmış, son 7 günde mesaj almamış Ankara müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış"],"unsure":null}
- **false_block** `combined-02` "Son 14 günde sepete ekleyip 30 gündür alışveriş yapmayan Ankara müşterileri" 
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-03` "30 gündür alışveriş yapmayan, 14 gündür mesaj almayan Bursa müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-05` "Customers in Ankara who bought in the last 60 days and have not been messaged in 14 days" 
  - {"brief":"Customers in Ankara who bought in the last 60 days and have not been messaged in 14 days","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["have not been messaged in 14 days"],"unsure":null}
- **false_block** `combined-06` "Ankara users who added to cart in the last 7 days but have not bought in 30 days" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-07` "14 gün içinde mesaj almamış ve 90 gün içinde alışveriş yapmış Antalya kullanıcıları" 
  - {"brief":"14 gün içinde mesaj almamış ve 90 gün içinde alışveriş yapmış Antalya kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Antalya","purchased_within_days":90},"unmatched":["recent messages"],"unsupported":["14 gün içinde mesaj almamış"],"unsure":null}
- **false_block** `combined-08` "Son 30 günde sepete ekleyip almayan, son 3 günde mesaj almamış Türkçe konuşan Adana müşterileri" 
  - "rejected: proposal failed validation: not_purchased_within_days must be between 1 and 365"
- **false_block** `combined-09` "Son 7 günde alışveriş yapan, son 30 günde sepete ekleyen İzmir kullanıcıları" 
  - {"brief":"Son 7 günde alışveriş yapan, son 30 günde sepete ekleyen İzmir kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"İzmir","purchased_within_days":7},"unmatched":null,"unsupported":["son 30 günde sepete ekleyen"],"unsure":null}
- **false_block** `noise-21` "Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine" 
  - {"brief":"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["cart activity"],"unsupported":[],"unsure":null}
- **false_block** `noise-24` "Tüm Ankara müşterilerine, yaş fark etmeksizin" 
  - {"brief":"Tüm Ankara müşterilerine, yaş fark etmeksizin","not_applied":null,"not_audience":null,"rule":{"city":"Ankara"},"unmatched":["age"],"unsupported":[],"unsure":null}
- **false_block** `noise-25` "Erkek kadın fark etmeksizin tüm Bursa müşterileri" 
  - {"brief":"Erkek kadın fark etmeksizin tüm Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["gender"],"unsupported":[],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri ve son 7 günde mesaj almamış olanlar","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar"],"unsure":null}
- **false_block** `refinement-19` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "7 gün yap"
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days"
- **false_block** `refinement-20` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "Türkçe konuşanlar"
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days"
- **false_block** `refinement-21` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "Bursa ve 14 gün"
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
- **false_block** `unseen_negation-08` "Son 30 gündür mağazamıza uğramayan Bursa müşterileri" 
  - {"brief":"Son 30 gündür mağazamıza uğramayan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":null,"unsupported":["Son 30 gündür mağazamıza uğramayan"],"unsure":null}
- **setup_failed** `content-03` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" 
  - "proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
