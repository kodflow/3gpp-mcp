# Validation Sentinel R17 ⇄ texte normatif 3GPP (3gpp-mcp)

Oracle: docs/inputs/sentinel_r17_events.json (218 events, 14 NF, scrapé de /docs/x2/r17-matrix/).
Vérification: la clause citée existe-t-elle comme clause indexée dans data/3gpp.duckdb ?

## Résumé

| Spec | Events | Clause exacte vérifiée | Statut |
|---|---|---|---|
| TS 33.128 | 93 | **93 / 93** | ✅ entièrement adossé au texte normatif |
| TS 33.108 | 125 | 26 / 125 | ⚠️ events réels mais réfs fines synthétiques |

## TS 33.108 — nature des 99 réfs non localisables

- **43 en « Annex »** : ops MAP (HLR) / tables d'annexe — aucune clause précise (Sentinel non plus).
- **82 numéros synthétiques** (ex. §10.5.1.2.1) : adressent une **puce en prose** dans une clause record parente (§10.5.1.2) qui, elle, EST indexée. Le numéro fin n'existe pas dans le doc 3GPP.

## Par NF (clause exacte présente dans l'index)

| NF | events | clause exacte ✓ | source |
|---|---|---|---|
| AMF | 13 | 13 ✅ | 33.128 |
| AUSF | 2 | 2 ✅ | 33.128 |
| HLR | 19 | 0 ⚠️ | 33.108(annex) |
| HSS | 14 | 0 ⚠️ | 33.108 |
| HSS-IMS | 11 | 0 ⚠️ | 33.108 |
| LMF | 2 | 2 ✅ | 33.128 |
| MME | 29 | 11 ⚠️ | 33.128+33.108 |
| MMS | 21 | 21 ✅ | 33.108 |
| PGW | 32 | 1 ⚠️ | 33.108 |
| PTC | 26 | 26 ✅ | 33.108 |
| SGW | 20 | 14 ⚠️ | 33.108 |
| SMF | 17 | 17 ✅ | 33.128 |
| SMSF | 2 | 2 ✅ | 33.128 |
| UDM | 10 | 10 ✅ | 33.128 |

## Conclusion

- L'**index est complet et correct** (93/93 events 33.128 retrouvés à la clause exacte).
- Le « 44 » initial était un **bug d'extraction POC** (X2-only, 6 NF), pas l'index.
- Le « 218 » de Sentinel est **réel mais partiellement curatorial** : les events 33.108 viennent de prose/annexes, avec des **réfs de clause synthétiques** non présentes dans le document 3GPP.
- **Fix retenu** : extracteur 33.128 (93, clause exacte) + extracteur prose 33.108 (events ancrés à la clause record PARENTE réelle §X.5.1.x + ops MAP d'annexe), mapping domaine→NE versionné, chaque event marqué confiance + source.
