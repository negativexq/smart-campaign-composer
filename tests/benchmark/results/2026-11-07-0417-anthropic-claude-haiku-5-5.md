# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. Before the schema rewrite that keeps Claude's filters, so Claude dropped filters it had chosen. The current report is [2026-11-07-0451-anthropic-claude-haiku-5-5.md](2026-11-07-0451-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-07 04:17, 1 runs of 246 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|222|2|0|0|20|1|0|0|0|1|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|53|0|0|0|4|0|0|0|0|0|
|tuning|169|2|0|0|16|1|0|0|0|1|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|7|0|0|0|0|1|0|0|0|0|
|behavior|13|1|0|0|7|0|0|0|0|0|
|city_forms|15|0|0|0|1|0|0|0|0|0|
|combined|4|0|0|0|5|0|0|0|0|0|
|content|17|0|0|0|0|0|0|0|0|1|
|injection|18|0|0|0|0|0|0|0|0|0|
|language|7|1|0|0|0|0|0|0|0|0|
|many_cities|13|0|0|0|0|0|0|0|0|0|
|no_audience|10|0|0|0|0|0|0|0|0|0|
|noise|23|0|0|0|3|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|18|0|0|0|4|0|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|22|0|0|0|0|0|0|0|0|0|
|unsupported|20|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `behavior-01` no_purchase_since_cart requires cart_added_within_days
- `behavior-02` no_purchase_since_cart requires cart_added_within_days
- `behavior-03` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `behavior-04` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other
- `behavior-05` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `behavior-15` no_purchase_since_cart requires cart_added_within_days
- `behavior-18` purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `combined-02` purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `combined-03` purchased_within_days and not_purchased_within_days contradict each other
- `combined-06` purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other
- `content-11` It mentions Kürze, which is not in the brief.; It mentions Kürze, which is not in the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-14` no_purchase_since_cart requires cart_added_within_days
- `refinement-10` city is "İzmir" but the brief does not name it (it names: Ankara)
- `refinement-19` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-20` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-21` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days
- `refinement-22` purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days

20 runs needed a repair, 226 did not.

## Everything that was not ok

- **silent_wrong** `behavior-21` "Hiç alışveriş yapmamış Ankara kullanıcıları" 
  - {"brief":"Hiç alışveriş yapmamış Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","not_purchased_within_days":365},"unmatched":null,"unsupported":[],"unsure":null}
- **silent_wrong** `language-07` "Almanca konuşan Ankara kullanıcıları" 
  - {"brief":"Almanca konuşan Ankara kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","language":"de"},"unmatched":null,"unsupported":[],"unsure":null}
- **false_block** `behavior-01` "Son 30 günde sepete ürün ekleyip almayan Ankara müşterileri" 
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-02` "Son 7 günde sepetini terk eden İzmir kullanıcıları" 
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `behavior-03` "Ankara users who abandoned their cart last week" 
  - "rejected: proposal failed validation: not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days"
- **false_block** `behavior-04` "Sepetinde ürün bırakan Bursa müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `behavior-05` "İstanbul users who added to cart in the last 14 days" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `behavior-15` "Sepete ekledi ama satın almadı: son 5 gün, Ankara" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `behavior-18` "Sepetinde ürün olan Ankara müşterileri, son 30 gün" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `city_forms-14` "Customers based in Adana, new arrivals" 
  - {"brief":"Customers based in Adana, new arrivals","not_applied":null,"not_audience":null,"rule":{"city":"Adana"},"unmatched":null,"unsupported":["new arrivals"],"unsure":null}
- **false_block** `combined-02` "Son 14 günde sepete ekleyip 30 gündür alışveriş yapmayan Ankara müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-03` "30 gündür alışveriş yapmayan, 14 gündür mesaj almayan Bursa müşterileri" 
  - "rejected: proposal failed validation: purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-05` "Customers in Ankara who bought in the last 60 days and have not been messaged in 14 days" 
  - {"brief":"Customers in Ankara who bought in the last 60 days and have not been messaged in 14 days","not_applied":null,"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["have not been messaged in 14 days"],"unsure":null}
- **false_block** `combined-06` "Ankara users who added to cart in the last 7 days but have not bought in 30 days" 
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `combined-07` "14 gün içinde mesaj almamış ve 90 gün içinde alışveriş yapmış Antalya kullanıcıları" 
  - {"brief":"14 gün içinde mesaj almamış ve 90 gün içinde alışveriş yapmış Antalya kullanıcıları","not_applied":null,"not_audience":null,"rule":{"city":"Antalya","purchased_within_days":90},"unmatched":["recent messages"],"unsupported":["14 gün içinde mesaj almamış"],"unsure":null}
- **false_block** `noise-21` "Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine" 
  - {"brief":"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":["cart activity"],"unsupported":[],"unsure":null}
- **false_block** `noise-24` "Tüm Ankara müşterilerine, yaş fark etmeksizin" 
  - {"brief":"Tüm Ankara müşterilerine, yaş fark etmeksizin","not_applied":null,"not_audience":null,"rule":{"city":"Ankara"},"unmatched":null,"unsupported":["yaş fark etmeksizin"],"unsure":null}
- **false_block** `noise-25` "Erkek kadın fark etmeksizin tüm Bursa müşterileri" 
  - {"brief":"Erkek kadın fark etmeksizin tüm Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa"},"unmatched":null,"unsupported":["Erkek kadın fark etmeksizin"],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri ve son 7 günde mesaj almamış olanlar da","not_applied":["'son 7 günde mesaj almamış olanlar da' asks to add people who received no campaign message in the last 7 days alongside the 60-day purchasers. The filters combine only with AND, so this addition cannot be expressed. Setting not_messaged_within_days to 7 would narrow the audience to purchasers who were not messaged, which is the opposite of adding people, so it was not applied."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar da"],"unsure":null}
- **false_block** `refinement-19` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "7 gün yap"
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `refinement-20` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "Türkçe konuşanlar"
  - "rejected: proposal failed validation: purchased_within_days must be between 1 and 365; not_purchased_within_days must be between 1 and 365; not_messaged_within_days must be between 1 and 365; no_purchase_since_cart requires cart_added_within_days; purchased_within_days and not_purchased_within_days contradict each other"
- **false_block** `refinement-21` "Son 30 günde sepete ekleyip almayan İstanbul müşterileri" + "Bursa ve 14 gün"
  - "rejected: proposal failed validation: no_purchase_since_cart requires cart_added_within_days"
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
- **provider_error** `content-06` "Ankara'daki müşterilere %30 indirim duyurusu" + "İzmir mağazamızda da geçerli olduğunu söyle"
  - "model output was cut off"
