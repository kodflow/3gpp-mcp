# TS 33.128 — events LI par release (R17/R18/R19)

Versions indexées: R17=17.20.0 R18=18.15.0 R19=19.6.0. ✓ = clause présente dans cette release.

| Clause | R17 | R18 | R19 | NF | Event |
|---|:--:|:--:|:--:|---|---|
| 6.2.2.2.10 |  | ✓ | ✓ | AMF | UE Configuration Update |
| 6.2.2.2.11 |  | ✓ | ✓ | AMF | Trace |
| 6.2.2.2.12 |  | ✓ | ✓ | AMF | UE policy transfer |
| 6.2.2.2.13 |  | ✓ | ✓ | AMF | Service Accept |
| 6.2.2.2.14 |  |  | ✓ | AMF | UE context update |
| 6.2.2.2.2 | ✓ | ✓ | ✓ | AMF | Registration |
| 6.2.2.2.3 | ✓ | ✓ | ✓ | AMF | Deregistration |
| 6.2.2.2.4 | ✓ | ✓ | ✓ | AMF | Location update |
| 6.2.2.2.5 | ✓ | ✓ | ✓ | AMF | Start of interception with registered UE |
| 6.2.2.2.6 | ✓ | ✓ | ✓ | AMF | AMF unsuccessful procedure |
| 6.2.2.2.7 | ✓ | ✓ | ✓ | AMF | AMF identifier association/deassociation |
| 6.2.2.2.8 | ✓ | ✓ | ✓ | AMF | Positioning info transfer |
| 6.2.2.2.9 | ✓ | ✓ | ✓ | AMF | UE Configuration Update |
| 6.2.3.2.10 |  |  | ✓ | SMF/UPF | Start of interception with connected ProSe remote UE |
| 6.2.3.2.2 | ✓ | ✓ | ✓ | SMF/UPF | PDU session establishment |
| 6.2.3.2.3 | ✓ | ✓ | ✓ | SMF/UPF | PDU session modification |
| 6.2.3.2.4 | ✓ | ✓ | ✓ | SMF/UPF | PDU session release |
| 6.2.3.2.5 | ✓ | ✓ | ✓ | SMF/UPF | Start of interception with an established PDU session |
| 6.2.3.2.6 | ✓ | ✓ | ✓ | SMF/UPF | SMF unsuccessful procedure |
| 6.2.3.2.7 | ✓ | ✓ | ✓ | SMF/UPF | MA PDU sessions |
| 6.2.3.2.8 | ✓ | ✓ | ✓ | SMF/UPF | PDU to MA PDU session modification |
| 6.2.3.2.9 |  |  | ✓ | SMF/UPF | ProSe remote UE report |
| 6.2.3.5.1 | ✓ | ✓ | ✓ | SMF/UPF | Packet data header reporting |
| 6.2.3.5.2 | ✓ | ✓ | ✓ | SMF/UPF | Fragmentation |
| 6.2.3.5.3 | ✓ | ✓ | ✓ | SMF/UPF | Packet Data Header Report (PDHR) |
| 6.2.3.5.4 | ✓ | ✓ | ✓ | SMF/UPF | Packet Data Summary Report (PDSR) |
| 6.3.2.2.10 |  | ✓ | ✓ | MME | Trace |
| 6.3.2.2.11 |  | ✓ | ✓ | MME | Service Accept |
| 6.3.2.2.2 | ✓ | ✓ | ✓ | MME | MME identifier association/deassociation |
| 6.3.2.2.3 | ✓ | ✓ | ✓ | MME | Attach |
| 6.3.2.2.4 | ✓ | ✓ | ✓ | MME | Detach |
| 6.3.2.2.5 | ✓ | ✓ | ✓ | MME | Tracking Area/EPS Location update |
| 6.3.2.2.6 | ✓ | ✓ | ✓ | MME | Start of interception with EPS attached UE |
| 6.3.2.2.7 | ✓ | ✓ | ✓ | MME | MME unsuccessful procedure |
| 6.3.2.2.8 | ✓ | ✓ | ✓ | MME | Positioning info transfer |
| 6.3.2.2.9 |  | ✓ | ✓ | MME | Handovers |
| 6.3.3.2.10 |  | ✓ |  | SGW/PGW and ePDG | EPS PDN unsuccessful procedure or SMF unsuccessful procedure in interworked EPS/5GS |
| 6.3.3.2.2 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | PDU Session Establishment message reporting PDU session establishment or PDN Connection establishment |
| 6.3.3.2.3 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | PDU Session Modification message reporting PDU session modification, PDN Connection modification or inter-system handover |
| 6.3.3.2.4 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | PDU Session Release message reporting PDU session release, PDN Connection release |
| 6.3.3.2.5 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | SMF Start of Interception with Already Established PDU Session message reporting Start of Interception with Already Established PDU Session or Start of Interception with Already Established PDN Connection |
| 6.3.3.2.6 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | MA PDU Session Establishment message reporting MA PDU session establishment or PDN Connection establishment as part of an MA PDU Session |
| 6.3.3.2.7 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | MA PDU Session Modification message reporting MA PDU session modification, modification of a PDN Connection associated to MA PDU session or inter-system handover |
| 6.3.3.2.8 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | MA PDU Session Release message reporting MA PDU session release or the release of a PDN Connection associated to an MA PDU session |
| 6.3.3.2.9 | ✓ | ✓ | ✓ | SGW/PGW and ePDG | SMF Start of Interception with Already Established MA PDU Session message reporting Start of Interception with Already Established MA PDU Session or Start of Interception with Already Established PDN Connection associated to an MA PDU Session |

## Ajoutés en R18 (7)
- 6.2.2.2.10 AMF/UE Configuration Update
- 6.2.2.2.11 AMF/Trace
- 6.2.2.2.12 AMF/UE policy transfer
- 6.2.2.2.13 AMF/Service Accept
- 6.3.2.2.10 MME/Trace
- 6.3.2.2.11 MME/Service Accept
- 6.3.2.2.9 MME/Handovers

## Ajoutés en R19 (3)
- 6.2.2.2.14 AMF/UE context update
- 6.2.3.2.10 SMF/UPF/Start of interception with connected ProSe remote UE
- 6.2.3.2.9 SMF/UPF/ProSe remote UE report

## R18 uniquement / restructurés en R19 (1)
- 6.3.3.2.10 SGW/PGW and ePDG/EPS PDN unsuccessful procedure or SMF unsuccessful procedure in interworked EPS/5GS
