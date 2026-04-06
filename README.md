# eebus-ha-bridge

Lokale Integration von EEBUS-fähigen Wärmepumpen (initial Vaillant aroTHERM plus) in Home Assistant. Bestehend aus einem Go-basierten Bridge-Dienst, der [eebus-go](https://github.com/enbility/eebus-go) einbettet und eine gRPC-API exponiert, sowie einer schlanken HA Custom Integration als Client.

## Status

In Planung — noch kein lauffähiger Code.

## Funktionsumfang (geplant)

- **Leistungsbegrenzung (LPC)** — §14a-konforme Laststeuerung über EEBUS
- **Leistungsmessung** — Elektrische Verbrauchsdaten der Wärmepumpe
- **Discovery & Pairing** — mDNS-Erkennung und SKI-basiertes Pairing über den HA Config Flow
- **Erweiterbar** — Architektur vorbereitet für zukünftige EEBUS-HVAC-Use-Cases

## Architektur

```
Home Assistant                    eebus-bridge (Go)         Vaillant VR940f
┌──────────────┐    gRPC    ┌──────────────────┐   SHIP   ┌──────────────┐
│ eebus custom │◄──────────►│ gRPC Server      │◄────────►│ aroTHERM plus│
│ integration  │            │ eebus-go (SHIP/  │          │ (EEBUS CS)   │
│ (Python)     │            │ SPINE embedded)  │          │              │
└──────────────┘            └──────────────────┘          └──────────────┘
```

## Dokumentation

- [Research — Integrationsansätze](eebus-research.md) — Analyse von 7 möglichen Ansätzen zur EEBUS-Integration
- [Design-Spec](docs/superpowers/specs/2026-04-06-eebus-bridge-design.md) — Detailliertes Design für den gewählten Ansatz (Go-Bridge + gRPC + HA Custom Integration)

## Lizenz

TBD
