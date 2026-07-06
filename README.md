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

### Diagnose: Live-Watcher

Zum schnellen Pruefen, welche Daten die Bridge aktuell liefert, gibt es jetzt
einen Live-Watcher im Stil eines Terminal-Dashboards:

```bash
cd eebus-bridge
go run ./cmd/eebus-watch --host 127.0.0.1 --port 50051 --ski <REMOTE-SKI>
```

Ohne `--ski` nimmt das Tool automatisch das erste gepaarte Geraet. Mit
`--register --ski <REMOTE-SKI>` registriert es den SKI bei der laufenden Bridge,
damit der SHIP/EEBUS-Verbindungsaufbau angestossen wird. Mit `--once` gibt es
nur einen Snapshot aus, mit `--debug` werden auch `NotFound`-/`Unavailable`-
Fehler angezeigt, und mit `--no-clear` bleibt der Verlauf im Terminal sichtbar.

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
| `select.eebus_compressor_flexibility` | select | OHPCF-Verdichter-Flexibilitaet: `on`/`paused`/`off`, nur vorhanden wenn WP ein Angebot meldet (experimentell) |

### Diagnose

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `binary_sensor.eebus_connected` | binary_sensor | EEBUS-Verbindungsstatus |
| `binary_sensor.eebus_heartbeat_ok` | binary_sensor | Heartbeat innerhalb Toleranz, standardmaessig deaktiviert |
| `sensor.eebus_compressor_flexibility_power_estimate` | sensor | OHPCF geschaetzte Leistung des Angebots (W), standardmaessig deaktiviert |
| `sensor.eebus_compressor_flexibility_power_max` | sensor | OHPCF maximale Leistung des Angebots (W), standardmaessig deaktiviert |

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

## Energiemanagement mit EEBUS verstehen

EEBUS ist mehr als die §14a-Notbremse. Mit den richtigen Hebeln wird die
Waermepumpe zu einem steuerbaren Baustein im Hausenergie-Optimum: PV-Ueberschuss
in den Warmwasser- und Pufferspeicher schieben, teure Netzbezugsspitzen kappen,
guenstige Strompreisfenster nutzen. Wichtig ist zu verstehen, **wer was
entscheidet** -- die Bridge, die Waermepumpe und ein Optimierer wie HAEO teilen
sich die Arbeit.

### Die zwei Steuerungs-Hebel

**1. Direkte Begrenzung (LPC)** -- Du (bzw. eine HA-Automatisierung) setzt eine
harte Leistungs-Obergrenze in Watt (`number.eebus_lpc_limit` + `switch.eebus_lpc_active`).
Der Wert ist frei variabel, nicht nur die §14a-Stufe von 4,2 kW.

> **Wichtig:** LPC ist eine *Decke*, kein *Boden*. Es begrenzt, was die
> Waermepumpe maximal ziehen darf -- es **zwingt sie nicht** zum Verbrauch. Um
> die WP aktiv zum Laden des Speichers bei Ueberschuss zu bewegen, brauchst du
> den zweiten Hebel.

**2. Kooperatives Netzsignal (MGCP-Grid-Provider, experimentell)** -- Die Bridge
meldet der Waermepumpe die Bilanz am Hausanschluss (negativ = Einspeisung =
Ueberschuss). Die Waermepumpe entscheidet dann mit ihrem **eigenen internen
Optimierer** (Vaillant §1.3.1, "Energiemanagement"), ob sie Warmwasser- und
Pufferspeicher mit dem Ueberschuss vorlaedt. Die Komfort- und Sicherheitslogik
bleibt komplett in der WP.

Die **+/- Grad-Einstellung in der myVAILLANT-App** ist dabei der eigentliche
Aggressivitaets-Regler: Sie legt fest, wie weit die WP bei Ueberschuss die
Soll-Temperaturen von Warmwasser und Heizung anhebt. Mehr Grad = mehr
Speicherkapazitaet fuer Solarstrom, aber auch hoehere Bereitschaftsverluste.
Diese Einstellung wird **in der App** vorgenommen, nicht ueber die Bridge --
die Bridge liefert nur das Netzsignal, das den Optimierer ueberhaupt erst
ausloest.

### Voraussetzung: das Commissioning-Gate in der App

Der §1.3.1-Pfad (Hebel 2) funktioniert erst, wenn die WP den Energiemanager
app-seitig akzeptiert hat. Das ist ein **manueller Nutzerschritt**, der nicht
von der Bridge erledigt werden kann:

1. **myVAILLANT-App → Einstellungen → Netzwerk → EEBUS → Verfuegbare Geraete:**
   die Bridge (`HomeAssistant_eebus-bridge`) auf **Vertrauenswuerdig** setzen,
   sodass sie unter *Trusted devices* erscheint. (Das ist zusaetzlich zum
   SHIP-/SKI-Vertrauen, das HA bereits herstellt -- beide Seiten muessen
   vertrauen.)
2. **Einstellungen → Regler → Energiemanagement:** die Schieber fuer **Heizung
   UND Warmwasser** aktivieren. Erst dieser Schalter laesst die WP an ein
   externes Netzsignal binden und das PV-Ueberschuss-Laden ausfuehren.
3. **+/- Grad** fuer Warmwasser und Heizung nach Wunsch einstellen (s.o.).

Ohne diese Schritte ignoriert die WP das Netzsignal, auch wenn die Bridge es
korrekt sendet.

> **Status:** Der MGCP-Grid-Provider ist experimentell
> (`experimental.mgcp_provider`). Dass der VR940 das gesendete Netzsignal nach
> korrektem Commissioning tatsaechlich konsumiert, ist noch nicht hardware-final
> bestaetigt; einzelne evcc-Nutzer berichten von Firmware-Grenzen bei den
> Energie-Use-Cases. LPC (Hebel 1) ist dagegen produktiv.

### Wo HAEO ins Spiel kommt

HAEO (Home Assistant Energy Optimization) bzw. ein gleichwertiger
Home-Assistant-seitiger Energie-Optimierer (z. B. EMHASS) ist das **Gehirn**
ueber den beiden Hebeln. Er nimmt
Prognosen (PV-Ertrag, Strompreis/Tarif, Hauslast) und rechnet per
Optimierung einen Fahrplan ueber einen Zeithorizont: *wann* und *wie viel*
jeder steuerbare Verbraucher laufen soll. Die Bridge ist der **Aktor**, der
diesen Plan in die WP uebersetzt.

Arbeitsteilung im Gesamtsystem:

| Komponente | Rolle | Entscheidet |
|-----------|-------|-------------|
| HAEO (Optimierer) | Planer | *Wann* und *wie viel* -- aus Prognose, Preis, Optimierung |
| WP-interner Optimierer (§1.3.1) | Komfort | Speicher-Lade-Logik, +/- Grad, Vorlauf |
| Bridge / LPC | Aktor / Schranke | Harte Grenzen, §14a, Plan-Umsetzung |

Zwei typische Kopplungen:

- **Fahrplan → LPC:** HAEO plant "WP zieht 12-14 Uhr X kW" → Automatisierung
  schreibt `number.eebus_lpc_limit = X` und schaltet das Limit ein. Direkte,
  vorhersehbare Steuerung.
- **Ueberschuss-Fahrplan → Netzsignal + LPC-Deckel:** HAEO bestimmt das
  Ueberschussfenster → die Bridge pusht das MGCP-Netzsignal, der WP-Optimierer
  laedt den Speicher, und LPC dient als Deckel gegen ungewollte
  Netzbezugsspitzen. Kooperativ, ueberlaesst der WP die Komfortlogik.

## Anwendungsbeispiele

### HAEO-Fahrplan auf LPC mappen

```yaml
automation:
  - alias: "HAEO-Plan an Waermepumpe"
    trigger:
      - platform: state
        entity_id: sensor.haeo_wp_plan_power   # geplante WP-Leistung (W)
    action:
      - service: number.set_value
        target:
          entity_id: number.eebus_lpc_limit
        data:
          value: "{{ states('sensor.haeo_wp_plan_power') | float(0) }}"
      - service: switch.turn_on
        target:
          entity_id: switch.eebus_lpc_active
```

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
4. Vaillant-Gateways erlauben nur **eine** EEBUS-Verbindung -- pruefen, dass kein
   anderer Energiemanager (z. B. sensoNET-Cloud) den Slot belegt
5. Bleibt die Verbindung in einer Reconnect-Schleife (Status erreicht nie
   `Trusted`), das SHIP-Logging aktivieren (siehe unten), um den Abbruchgrund
   zu sehen

</details>

<details>
<summary>SHIP-Handshake debuggen</summary>

Die internen ship-go/eebus-go-Logs sind standardmaessig stumm. Zum Anzeigen der
SHIP-Handshake-Fehler und Abbruchgruende:

```yaml
logging:
  ship_log: true     # Debug/Info/Error -- enthaelt den Abbruchgrund
  # ship_trace: true # rohe Nachrichten pro Paket, sehr ausfuehrlich
```

Oder per Umgebungsvariable (z. B. in `docker-compose.yml`):

```
EEBUS_SHIP_LOG=true
```

Anschliessend im Bridge-Log nach `[SHIP DEBUG]`/`[SHIP ERROR]` suchen, z. B.:

- `SHIP handshake error: ... handshake timeout` -- Geraet antwortet nicht /
  Pairing geraeteseitig nicht bestaetigt
- `Node rejected by application` -- Geraet hat den Bridge-SKI aktiv abgelehnt
- TLS/Zertifikatsfehler -- SKI stimmt nicht ueberein

</details>

<details>
<summary>Keine Messwerte</summary>

- EEBUS-Messwerte kommen ca. alle 60 Sekunden
- Pruefen ob `binary_sensor.eebus_connected` "on" ist
- Bridge-Log auf Fehlermeldungen pruefen

</details>

## Lizenz

MIT License -- siehe [LICENSE](LICENSE).
