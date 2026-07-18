# EEBUS Bridge → Home Assistant

[![HACS Custom](https://img.shields.io/badge/HACS-Custom-41BDF5.svg?style=for-the-badge)](https://github.com/hacs/integration) [![GitHub Release](https://img.shields.io/github/v/release/volschin/eebus-ha-bridge?style=for-the-badge)](https://github.com/volschin/eebus-ha-bridge/releases) [![License](https://img.shields.io/badge/License-MIT-green?style=for-the-badge)](LICENSE) [![Quality Scale](https://img.shields.io/badge/Quality%20Scale-Platinum-E5E4E2?style=for-the-badge)](https://www.home-assistant.io/docs/quality_scale/)

[![Tests](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/ci.yml?branch=main&style=for-the-badge&label=Tests)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/ci.yml) [![HACS Validation](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hacs.yml?branch=main&style=for-the-badge&label=HACS)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hacs.yml) [![Hassfest](https://img.shields.io/github/actions/workflow/status/volschin/eebus-ha-bridge/hassfest.yml?branch=main&style=for-the-badge&label=Hassfest)](https://github.com/volschin/eebus-ha-bridge/actions/workflows/hassfest.yml)

Lokale Integration von EEBUS-faehigen Waermepumpen in Home Assistant ueber das **EEBUS-Protokoll** (SHIP + SPINE) -- ohne Cloud, ohne mypyllant-Abhaengigkeit fuer Lastmanagement.

## Features

- **Leistungsbegrenzung (LPC)** -- Paragraph-14a-konforme Laststeuerung via EEBUS
- **Leistungsmessung** -- Elektrische Verbrauchsdaten der Waermepumpe in Echtzeit
- **Discovery & Pairing** -- mDNS-Erkennung und SKI-basiertes Pairing ueber den HA Config Flow
- **Heartbeat-Ueberwachung** -- Sicherheitsrelevanter EEBUS-Heartbeat mit Failsafe-Fallback
- **Warmwasser-Entitaet** -- Home-Assistant-`water_heater` mit Ist- und
  Solltemperatur, dynamischen Min/Max/Schritt-Grenzen und Betriebsart
- **Warmwasser-Isttemperatur** -- Lokales Push-Monitoring ueber den EEBUS
  `monitoringOfDhwTemperature`-Use-Case
- **Warmwasser-Boost und Betriebsart** -- Lokales Aktivieren der Einmalladung
  und Auswahl der vom Geraet angebotenen DHW-Betriebsarten
- **Raumheizung** -- Home-Assistant-`climate` mit Ist-/Solltemperatur,
  dynamischen Geraetegrenzen und den angebotenen Modi `auto`/`on`/`off`
- **Geraete-Betriebszustand** -- Read-only Diagnose des standardisierten EEBUS
  `DeviceDiagnosis.operatingState`, inklusive unbekannter zukuenftiger Werte
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

Bestehende Installationen, die gRPC auf `127.0.0.1` oder `::1` binden,
benoetigen keine Migration: Ohne weitere Konfiguration bleibt der Modus
`loopback` aktiv und verwendet lokal weiterhin Plaintext. Ein Bind auf
`0.0.0.0`, `::` oder eine LAN-Adresse wird dagegen ohne `tls_token` abgelehnt.
Die Bridge liest `config.yaml` strikt: unbekannte YAML-Schluessel oder falsch
geschriebene Feldnamen stoppen den Start mit einem Parse-Fehler, statt
stillschweigend ignoriert zu werden.

### Migration: Bridge auf einem entfernten Host

Remote-gRPC verwendet ein Serverzertifikat und ein Token. Zertifikat,
Private Key und Token liegen als Dateien auf dem Bridge-Host; Home Assistant
erhaelt nur das CA-Zertifikat und das Token. Der Private Key darf den
Bridge-Host nie verlassen.

1. Eine eigene CA und ein Serverzertifikat erstellen. Der Hostname oder die
   IP-Adresse, die spaeter in Home Assistant eingetragen wird, muss als SAN im
   Serverzertifikat stehen. Beispiel mit OpenSSL (Hostname/IP anpassen):

   ```bash
   install -d -m 700 grpc-secrets
   openssl genrsa -out grpc-secrets/ca.key 4096
   openssl req -x509 -new -sha256 -days 3650 \
     -key grpc-secrets/ca.key -out grpc-secrets/ca.crt \
     -subj "/CN=EEBUS Bridge CA"
   openssl genrsa -out grpc-secrets/server.key 3072
   openssl req -new -key grpc-secrets/server.key \
     -out grpc-secrets/server.csr -subj "/CN=eebus-bridge" \
     -addext "subjectAltName=DNS:eebus-bridge,IP:192.168.1.50"
   openssl x509 -req -sha256 -days 825 \
     -in grpc-secrets/server.csr -CA grpc-secrets/ca.crt \
     -CAkey grpc-secrets/ca.key -CAcreateserial \
     -copy_extensions copy -out grpc-secrets/server.crt
   openssl rand -hex 32 > grpc-secrets/token
   chmod 600 grpc-secrets/ca.key grpc-secrets/server.key grpc-secrets/token
   ```

2. Die Dateien read-only in den Bridge-Container mounten und `grpc` in
   `config.yaml` konfigurieren:

   ```yaml
   grpc:
     bind: "0.0.0.0"
     port: 50051
     enable_reflection: false
     security:
       mode: "tls_token"
       tls_cert_file: "/etc/eebus-bridge/grpc/server.crt"
       tls_key_file: "/etc/eebus-bridge/grpc/server.key"
       token_file: "/etc/eebus-bridge/grpc/token"
   ```

   Fuer Docker Compose beispielsweise zusaetzlich:

   ```yaml
   volumes:
     - ./grpc-secrets:/etc/eebus-bridge/grpc:ro
   ```

3. TCP-Port 50051 nur fuer den Home-Assistant-Host in der Firewall freigeben
   und die Bridge neu starten. Bei fehlenden/unlesbaren Dateien oder einem
   leeren Token verweigert die Bridge den Start.

4. In Home Assistant die EEBUS-Integration neu einrichten oder ueber
   **Configure** rekonfigurieren:

   - **Bridge Host:** exakt der DNS-Name oder die IP aus dem Zertifikat-SAN
   - **Security mode:** `TLS + token`
   - **TLS CA certificate:** kompletter PEM-Inhalt von `ca.crt`
   - **Authentication token:** Inhalt der Datei `token`

   Nach einer Token-Rotation startet ein `UNAUTHENTICATED`-Fehler automatisch
   den Reauthentication Flow. Alternativ koennen CA und Token ueber
   **Configure** ersetzt werden; ein leeres Tokenfeld behaelt dort das bisherige
   Token.

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
Fuer eine entfernte Bridge dieselben Dateien verwenden wie der sichere Kanal:

```bash
go run ./cmd/eebus-watch --host eebus-bridge --port 50051 \
  --security-mode tls_token --tls-ca-file ./grpc-secrets/ca.crt \
  --token-file ./grpc-secrets/token --ski <REMOTE-SKI>
```

Der Home-Assistant-Diagnosedownload enthaelt zusaetzlich eine getypte,
geraetebezogene Betriebsprojektion: Readiness und Recovery-Versuche,
Eventrevisionen mit Drop-/Resync-Zaehlern, Stream-Reconnects,
Snapshot-Laufzeit sowie Provider-Freshness. Vollstaendige SKIs, Tokens,
PEM-Inhalte und private Pfade werden dabei nicht ausgegeben.

## Einrichtung

1. **Settings** > **Devices & Services** > **Add Integration**
2. Nach **EEBUS** suchen
3. **Bridge-Host** und **Bridge-Port** eingeben (Standard: `localhost:50051`)
4. Sicherheitsmodus waehlen. Fuer eine lokale Loopback-Verbindung ist keine
   weitere Eingabe noetig; fuer Remote-Verbindungen CA-Zertifikat und Token
   eingeben.
5. Die Integration testet den gesicherten Kanal zur Bridge.
6. **Geraete-SKI** eingeben (wird in der Bridge-Log beim Discovery angezeigt)
7. In der **myVaillant-App** das Pairing bestaetigen

### Rekonfiguration

Bridge-Adresse, Sicherheitsmodus, CA-Zertifikat oder Token aendern:
**Settings** > **Devices & Services** > **EEBUS** > **Configure**

### Entfernen

**Settings** > **Devices & Services** > **EEBUS** > **Delete**

## Daten-Aktualisierung

Die Integration nutzt **gRPC Streaming** (Server-Sent Events) fuer Echtzeit-Updates. Bei Stream-Abbruch wechselt sie automatisch auf **Polling** (5-Minuten-Intervall) und verbindet den Stream im Hintergrund neu.

- **Leistungsmessung:** Event-basiert (ca. alle 60s vom Inverter)
- **LPC-Limits:** Event-basiert (bei Aenderung)
- **Warmwasser-Solltemperatur:** Event-basiert (bei Aenderung)
- **Warmwasser-Boost/Betriebsart:** Event-basiert (bei Aenderung)
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
| `water_heater.eebus_domestic_hot_water` | water_heater | Warmwasser mit Ist-/Solltemperatur und Betriebsart; Grenzen und Optionen kommen vom Geraet |
| `climate.eebus_room_heating` | climate | Raumheizung mit Ist-/Solltemperatur und `auto`/`on`/`off`; Grenzen und Optionen kommen vom Geraet |
| `switch.eebus_lpc_active` | switch | Limit aktivieren/deaktivieren |
| `switch.eebus_dhw_boost` | switch | Warmwasser-Einmalladung aktivieren/deaktivieren |
| `select.eebus_compressor_flexibility` | select | OHPCF-Verdichter-Flexibilitaet: `on`/`paused`/`off`, nur vorhanden wenn WP ein Angebot meldet |

### Diagnose

| Entity | Typ | Beschreibung |
|--------|-----|-------------|
| `binary_sensor.eebus_connected` | binary_sensor | EEBUS-Verbindungsstatus |
| `binary_sensor.eebus_heartbeat_ok` | binary_sensor | Heartbeat innerhalb Toleranz, standardmaessig deaktiviert |
| `sensor.eebus_device_operating_state` | sensor | EEBUS-Geraete-Betriebszustand, z. B. `normalOperation` |
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

- **HVAC-Steuerung:** Raumheizung wird als `climate.eebus_room_heating` exponiert (Ist-/Solltemperatur, Modi `auto`/`on`/`off`). Kuehlung, Zeitprogramme und ein belastbarer Heizaktivitaetsstatus (`hvac_action`) werden vom VR940 nicht angeboten.
- **Kein Auto-Discovery in HA:** Die Bridge-Adresse muss manuell eingegeben werden. Die EEBUS-Discovery (mDNS) findet Waermepumpen, aber die Bridge selbst wird nicht von HA entdeckt.
- **Re-Pairing bei Zertifikatsverlust:** Wird das Bridge-Zertifikat geloescht, aendert sich der SKI. Erneutes Pairing in der myVaillant-App noetig.
- **Heartbeat bei Bridge-Ausfall:** Die Waermepumpe erkennt den Heartbeat-Timeout (max 2 min) und faellt auf den Failsafe-Wert zurueck.

## Troubleshooting

<details>
<summary>Bridge nicht erreichbar</summary>

1. Bridge-Container laeuft? `docker ps | grep eebus-bridge`
2. Port 50051 erreichbar? `grpcurl -plaintext localhost:50051 list`
3. Bridge-Log pruefen: `docker logs eebus-bridge`
4. Bei Startfehlern nach `parsing config file` suchen. `config.yaml` akzeptiert
   nur bekannte Schluessel; Tippfehler muessen korrigiert oder entfernt werden.

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
- Ein eingebauter Watchdog erkennt selbststaendig, wenn nach einem SHIP-Reconnect
  die SPINE-Entity-Bindung haengen bleibt (Symptom: `no compatible entity` /
  `no remote entity found` im Log trotz getrustetem Geraet). Bleiben laenger als
  10 Minuten keine erfolgreichen Messwert-Reads aus, meldet der Docker
  `HEALTHCHECK` den Container als unhealthy und die Bridge beendet sich selbst
  fuer einen Neustart (`restart: unless-stopped` in `docker-compose.yml` startet
  sie automatisch neu). Kein manuelles Eingreifen noetig; bei Bedarf laesst sich
  der Status vorab pruefen mit `docker inspect --format='{{.State.Health.Status}}' eebus-bridge`.

</details>

## Lizenz

MIT License -- siehe [LICENSE](LICENSE).
