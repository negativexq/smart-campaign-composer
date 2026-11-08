# Benchmark: anthropic claude-haiku-5-5

> **Superseded.** Kept as history, not a current result. A full run on the same code as the current one, scored by a check that still wanted the link itself in the SMS. Since commit f239dd3 an SMS carries {{link}} in its place, so content-17 counted as silently skipped; the check now accepts {{link}} in the SMS and the run was repeated. The current report is [2026-11-09-0220-anthropic-claude-haiku-5-5.md](2026-11-09-0220-anthropic-claude-haiku-5-5.md); see the [index](README.md).

2026-11-09 02:15, 1 runs of 247 cases.

## Overall

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|all|242|0|0|1|2|2|0|0|0|0|

## Tuning and held-out cases

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|held out|56|0|0|0|1|0|0|0|0|0|
|tuning|186|0|0|1|1|2|0|0|0|0|

## By category

| |ok|silent_wrong|forbidden_text|silent_skip|false_block|visible_miss|missed_note|rejected|setup_failed|provider_error|
|---|---|---|---|---|---|---|---|---|---|---|
|ambiguous|7|0|0|0|0|1|0|0|0|0|
|behavior|21|0|0|0|0|0|0|0|0|0|
|city_forms|15|0|0|0|1|0|0|0|0|0|
|combined|9|0|0|0|0|0|0|0|0|0|
|content|17|0|0|1|0|0|0|0|0|0|
|injection|18|0|0|0|0|0|0|0|0|0|
|language|9|0|0|0|0|0|0|0|0|0|
|many_cities|13|0|0|0|0|0|0|0|0|0|
|no_audience|10|0|0|0|0|0|0|0|0|0|
|noise|26|0|0|0|0|0|0|0|0|0|
|other_places|14|0|0|0|0|0|0|0|0|0|
|refinement|20|0|0|0|1|1|0|0|0|0|
|time_windows|21|0|0|0|0|0|0|0|0|0|
|unseen_negation|22|0|0|0|0|0|0|0|0|0|
|unsupported|20|0|0|0|0|0|0|0|0|0|

## What the backend sent back to the model

- `content-02` It mentions Kürze, which is not in the brief.
- `content-03` It mentions Ihren, which is not in the brief.
- `injection-11` language is "en" but the brief does not ask for it
- `injection-12` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-13` city is "Ankara" but the brief does not name it (it names: no city)
- `injection-14` purchased_within_days is 30 but the brief does not say that; use the time it gives, or record an assumption if it gives none
- `refinement-09` purchased_within_days is 14 but the brief does not say that; use the time it gives, or record an assumption if it gives none
- `refinement-10` city is "İzmir" but the brief does not name it (it names: Ankara)

8 runs needed a repair, 239 did not.

## Everything that was not ok

- **silent_skip** `content-17` "Ankara müşterilerine https://example.com/yaz adresindeki yaz kampanyasını duyur" 
  - {"body":"Merhaba {{first_name}},\n\nYaz kampanyamızı Ankara'daki müşterilerimize duyurmaktan memnuniyet duyarız. Kampanya detaylarını görmek için aşağıdaki butona tıklayabilirsiniz.\n\nİyi alışverişler dileriz.","notes":[],"sms":"Merhaba {{first_name}}, Ankara'da yaz kampanyamız başladı! Detaylar için: {{link}}","subject":"Ankara'da yaz kampanyası başladı","url":"https://example.com/yaz"}
- **false_block** `city_forms-14` "Customers based in Adana, new arrivals" 
  - {"brief":"Customers based in Adana, new arrivals","not_applied":null,"not_audience":null,"rule":{"city":"Adana"},"unmatched":null,"unsupported":["new arrivals"],"unsure":null}
- **false_block** `refinement-16` "Son 60 günde alışveriş yapan Ankara müşterileri" + "son 7 günde mesaj almamış olanlar da"
  - {"brief":"Son 60 günde alışveriş yapan Ankara müşterileri, son 7 günde mesaj almamış olanlar da","not_applied":["The refinement adds people who have not received a message in the last 7 days with \"da\" (also). Filters are combined with AND, so a filter would narrow the audience instead of adding to it, and adding a second group needs a union that no filter expresses. The segment is unchanged and not_messaged_within_days is not set."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":["recent messages"],"unsupported":["son 7 günde mesaj almamış olanlar da"],"unsure":null}
- **visible_miss** `ambiguous-07` "Bizi unutan Bursa müşterileri" 
  - {"brief":"Bizi unutan Bursa müşterileri","not_applied":null,"not_audience":null,"rule":{"city":"Bursa","not_purchased_within_days":90},"unmatched":null,"unsupported":[],"unsure":["Bizi unutan"]}
- **visible_miss** `refinement-05` "Ankara customers who bought in the last 60 days" + "add people in İzmir too"
  - {"brief":"Ankara customers who bought in the last 60 days","not_applied":["The refinement asks to add people in İzmir, which this audience cannot do. The city filter holds one city, so the proposal stays limited to Ankara. To reach İzmir, create a separate campaign for it."],"not_audience":null,"rule":{"city":"Ankara","purchased_within_days":60},"unmatched":null,"unsupported":[],"unsure":null}
