# EEBUS Bridge → Home Assistant

[![HACS Custom](https://img.shields.io/badge/HACS-Custom-41BDF5.svg?style=for-the-badge)](https://github.com/hacs/integration) [![GitHub Release](https://img.shields.io/github/v/release/volschin/eebus-ha-bridge?style=for-the-badge)](https://github.com/volschin/eebus-ha-bridge/releases) [![License](https://img.shields.io/github/license/volschin/eebus-ha-bridge?style=for-the-badge)](LICENSE) [![Quality Scale](https://img.shields.io/badge/Quality%20Scale-Gold-FFD700?style=for-the-badge)](https://www.home-assistant.io/docs/quality_scale/)

[![Tests](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/ci.yml?branch=main&style=for-the-badge&label=Tests)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/ci.yml) [![HACS Validation](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hacs.yml?branch=main&style=for-the-badge&label=HACS)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hacs.yml) [![Hassfest](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hassfest.yml?branch=main&style=for-the-badge&label=Hassfest)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hassfest.yml)

Lokale Integration von EEBUS-faehigen Waermepumpen in Home Assistant ueber das **EEBUS-Protokoll** (SHIP + SPINE) -- ohne Cloud, ohne mypyllant-Abhaengigkeit fuer Lastmanagement.

## Features

- **Leistungsbegrenzung (LPC)** -- Paragraph-14a-konforme Laststeuerung via EEBUS
- **Leistungsmessung** -- Elektrische Verbrauchsdaten der Waermepumpe in Echtzeit
- **Discovery & Pairing** -- mDNS-Erkennung und SKI-basiertes Pairing ueber den HA Config Flow
- **Heartbeat-Ueberwachung** -- Sicherheitsrelevanter EEBUS-Heartbeat mit Failsafe-Fallback
- **Energy Dashboard** -- Volle Integration mit dem HA Energy Dashboard
- **Erweiterbar** -- Architektur vorbereitet fuer zukuenftige EEBUS-HVAC-Use-Cases

## Architektur

```
Home Assistant                    eebus-bridge (Go)         Vaillant VR940f
+----------------+    gRPC    +------------------+   SHIP   +--------------+
| eebus custom   |<---------->| gRPC Server      |<-------->| aroTHERM plus|
| integration    |            | eebus-go (SHIP/  |          | (EEBUS CS)   |
| (Python)       |            | SPINE embedded)  |          |              |
+----------------+            +------------------+          +--------------+
```

## Installation

### HACS (empfohlen)

1. HACS in Home Assistant oeffnen
2. **Integrations** > drei Punkte oben rechts > **Custom repositories**
3. Repository-URL einfuegen: `https://github.com/volschin/eebus-ha-bridge`
4. Kategorie: **Integration** > **Add**
5. Nach "EEBUS" suchen und **Download** klicken
6. Home Assistant neustarten

### Manuelle Installation

1. Neuestes Release von der [Releases-Seite](https://github.com/volschin/eebus-ha-bridge/releases) herunterladen
2. Den Ordner `eebus` nach `custom_components/eebus/` kopieren
3. Home Assistant neustarten

### Bridge-Setup

Der Go-basierte Bridge-Dienst muss separat laufen (Docker empfohlen):

```bash
docker-compose up -d eebus-bridge
```

Alternativ als Binary:

```bash
./eebus-bridge --config config.yaml
```

## Einrichtung

1. **Settings** > **Devices & Services** > **Add Integration**
2. Nach **EEBUS** suchen
3. **Bridge-Host** und **Bridge-Port** eingeben (Standard: `localhost:50051`)
4. Die Integration testet die Verbindung zur Bridge
5. **Geraete-SKI** eingeben (wird in der Bridge-Log beim Discovery angezeigt)
6. In der **myVaillant-App** das Pairing bestaetigen

### Rekonfiguration

Bridge-Adresse aendern: **Settings** > **Devices & Services** > **EEBUS** > **Configure**

### Entfernen

**Settings** > **Devices & Services** > **EEBUS** > **Delete**

## Daten-Aktualisierung

Die Integration nutzt **gRPC Streaming** (Server-Sent Events) fuer Echtzeit-Updates. Bei Stream-Abbruch wechselt sie automatisch auf **Polling** (30s Intervall) und verbindet den Stream im Hintergrund neu.

- **Leistungsmessung:** Event-basiert (ca. alle 60s vom Inverter)
- **LPC-Limits:** Event-basiert (bei Aenderung)
- **Heartbeat:** Alle 4 Sekunden (im Bridge, nicht in HA)

## Verfuegbare Entities

### Sensoren

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `sensor.eebus_power_consumption` | sensor | Aktuelle elektr. Leistung (W) |
| `sensor.eebus_consumption_limit` | sensor | Aktuell gesetztes LPC-Limit (W), readonly |

### Steuerung

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `number.eebus_lpc_limit` | number | LPC-Limit setzen (W) |
| `number.eebus_failsafe_limit` | number | Failsafe-Grenze (W), standardmaessig deaktiviert |
| `switch.eebus_lpc_active` | switch | Limit aktivieren/deaktivieren |
| `switch.eebus_heartbeat` | switch | Heartbeat an/aus, standardmaessig deaktiviert |

### Diagnose

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `binary_sensor.eebus_connected` | binary_sensor | EEBUS-Verbindungsstatus |
| `binary_sensor.eebus_heartbeat_ok` | binary_sensor | Heartbeat innerhalb Toleranz, standardmaessig deaktiviert |

## Unterstuetzte Geraete

### Kompatible Modelle

| Hersteller | Modell | Gateway | Getestet |
|-----------|--------|---------|----------|
| Vaillant | aroTHERM plus | VR940f | Primaeres Ziel |
| Vaillant | aroTHERM plus | VR920/VR921 | Kompatibel |

### Voraussetzungen

- Vaillant Gateway (VR920/VR921/VR940f) mit aktiviertem EEBUS
- Gateway und eebus-bridge im selben Netzwerk
- Docker oder Go 1.22+ fuer den Bridge-Dienst

### Nicht unterstuetzt

- HVAC-Steuerung (Betriebsmodi, Sollwerte) -- Vaillant exponiert diese nicht ueber EEBUS
- Geraete ohne EEBUS-Schnittstelle

## Anwendungsbeispiele

### PV-gefuehrte Lastbegrenzung

```yaml
automation:
  - alias: "PV-Ueberschuss an Waermepumpe"
    trigger:
      - platform: numeric_state
        entity_id: sensor.pv_ueberschuss
        above: 2000
    action:
      - service: number.set_value
        target:
          entity_id: number.eebus_lpc_limit
        data:
          value: "{{ states('sensor.pv_ueberschuss') | float }}"
      - service: switch.turn_on
        target:
          entity_id: switch.eebus_lpc_active
```

### Paragraph-14a-Notbremse

```yaml
automation:
  - alias: "Paragraph 14a Leistungsbegrenzung"
    trigger:
      - platform: state
        entity_id: binary_sensor.netzbetreiber_signal
        to: "on"
    action:
      - service: number.set_value
        target:
          entity_id: number.eebus_lpc_limit
        data:
          value: 4200
      - service: switch.turn_on
        target:
          entity_id: switch.eebus_lpc_active
```

## Bekannte Einschraenkungen

- **Keine HVAC-Steuerung:** Vaillant exponiert Betriebsmodi und Sollwerte nicht ueber EEBUS. Dafuer weiterhin mypyllant nutzen.
- **Kein Auto-Discovery in HA:** Die Bridge-Adresse muss manuell eingegeben werden. Die EEBUS-Discovery (mDNS) findet Waermepumpen, aber die Bridge selbst wird nicht von HA entdeckt.
- **Re-Pairing bei Zertifikatsverlust:** Wird das Bridge-Zertifikat geloescht, aendert sich der SKI. Erneutes Pairing in der myVaillant-App noetig.
- **Heartbeat bei Bridge-Ausfall:** Die Waermepumpe erkennt den Heartbeat-Timeout (max 2 min) und faellt auf den Failsafe-Wert zurueck.

## Troubleshooting

<details>
<summary>Bridge nicht erreichbar</summary>

1. Bridge-Container laeuft? `docker ps | grep eebus-bridge`
2. Port 50051 erreichbar? `grpcurl -plaintext localhost:50051 list`
3. Bridge-Log pruefen: `docker logs eebus-bridge`

</details>

<details>
<summary>Pairing schlaegt fehl</summary>

1. SKI in der Bridge-Log pruefen (wird beim Start ausgegeben)
2. In der myVaillant-App unter Netzwerk > EEBUS den Bridge-SKI bestaetigen
3. Sicherstellen, dass beide Geraete im selben Netzwerk sind

</details>

<details>
<summary>Keine Messwerte</summary>

- EEBUS-Messwerte kommen ca. alle 60 Sekunden
- Pruefen ob `binary_sensor.eebus_connected` "on" ist
- Bridge-Log auf Fehlermeldungen pruefen

</details>

## Lizenz

MIT License -- siehe [LICENSE](LICENSE).
