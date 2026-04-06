# Mögliche Home‑Assistant‑Integrationsansätze für Vaillant aroTHERM plus über EEBUS

## Überblick

Vaillant‑Wärmepumpen wie die aroTHERM plus kommunizieren im Kern über den proprietären eBUS, stellen aber für externe Energiemanagement‑Systeme zunehmend eine EEBUS‑Schnittstelle bereit, typischerweise über Internet‑Gateways wie VR920/VR921/VR940f und die myVaillant‑Plattform. EEBUS selbst ist ein offenes, auf IP basierendes Protokoll‑Set (SHIP + SPINE), das Lastmanagement‑Use‑Cases wie Leistungsbegrenzung (LPC), Messwerte und Statusmeldungen standardisiert und speziell für Energiemanagement und §14a‑Szenarien positioniert ist.[^1][^2][^3][^4][^5][^6]

Für Home Assistant existiert bislang keine allgemeine, produktionsreife EEBUS‑Integration, und Community‑Diskussionen verweisen explizit darauf, dass es (Stand heute) keinen etablierten Python‑EEBUS‑Stack gibt; als praktikabler Weg wird meist ein separater Dienst auf Basis der Go‑Bibliothek eebus‑go empfohlen, der dann via gRPC/MQTT von Home Assistant angebunden wird. Auf dieser Grundlage lassen sich mehrere, teils kombinierbare Architekturansätze entwerfen, um eine Vaillant‑Wärmepumpe lokal über EEBUS in Home Assistant zu integrieren.[^7][^8][^1]

## Voraussetzungen auf Vaillant‑Seite

Mehrere Quellen zeigen, dass EEBUS bei Vaillant über ein Internet‑Gateway (z.B. VR920, VR921 oder VR940f) und entsprechende Systemregler wie multiMatic oder sensoCOMFORT bereitgestellt wird. Die Aktivierung erfolgt typischerweise in der myVaillant‑App unter Netzwerkeinstellungen, wo die EEBUS‑Schnittstelle eingeschaltet und ein externer EEBUS‑Partner (z.B. gridBox, HEMS) über die SKI gekoppelt wird. Für neuere aroTHERM‑Generationen werden in Support‑Dokumenten Mindest‑Firmwarestände genannt, ab denen die Wärmepumpen als EEBUS‑Verbraucher auftreten und Use‑Cases wie Lastreduzierung (LPC) unterstützt werden.[^3][^4]

Aus praktischer Sicht bedeutet das: Für eine lokale EEBUS‑Integration mit Home Assistant wird in der Regel ein Vaillant‑Gateway mit aktueller Firmware und aktivierter EEBUS‑Option benötigt, das die Wärmepumpe im Heimnetz via IP (WebSocket/TLS) sichtbar macht. Gleichzeitig bleibt eBUS als interne Bus‑Technologie erhalten und kann alternativ weiterhin über ebusd‑basierte Lösungen genutzt werden, die heute schon in Home Assistant integriert sind, allerdings ohne die standardisierten EEBUS‑Use‑Cases.[^9][^10][^2][^4][^3]

## Option 1: Externer Go‑Dienst mit eebus‑go + MQTT‑Bridge

Die Go‑Bibliothek eebus‑go stellt eine umfassende Implementierung von SHIP 1.0.1 und großen Teilen von SPINE (aktuell 1.3.0) bereit, inklusive Zertifikats‑Handling, mDNS/DNS‑SD‑Erkennung, WebSocket‑Verbindungen, Pairing‑Mechanismen und Use‑Case‑Abstraktionen für Akteure wie Energiemanagementsysteme, Ladeboxen und Wärmepumpen. Im Repository finden sich Beispielprogramme (Controlbox, HEMS, EVSE), die zeigen, wie ein Dienst einen lokalen EEBUS‑Serverport bereitstellt, Zertifikate generiert, einen entfernten EEBUS‑Partner per SKI adressiert und anschließend Use‑Cases wie LPC‑Limits sendet oder Messwerte empfängt.[^8][^1]

Ein naheliegender Architekturansatz ist daher:

- Ein separater Container/Dienst im LAN (z.B. Docker auf demselben Host wie Home Assistant), der eebus‑go nutzt und die Vaillant‑Wärmepumpe über das Gateway per EEBUS koppelt.
- Der Go‑Dienst übersetzt EEBUS‑Use‑Cases in interne Datenstrukturen und publiziert diese als MQTT‑Topics (z.B. `vaillant/eebus/heatpump/power`, `…/flow_temperature`, `…/lpc_limit`), inklusive Retain‑Flags und klaren Topic‑Namensschemata.
- Befehle aus Home Assistant (z.B. Ziel‑Vorlauftemperatur, Betriebsmodus, manuelle Leistungsbegrenzung) werden ebenfalls via MQTT angenommen und durch den Dienst in passende EEBUS‑Kommandos (z.B. Setzen von Zielwerten oder LPC‑Parametern) übersetzt.

Home Assistant kann diese Topics mit der bestehenden MQTT‑Integration und optional via MQTT Discovery automatisch in `climate`‑, `sensor`‑, `binary_sensor`‑ und `select`‑Entitäten abbilden, ohne EEBUS im Core verstehen zu müssen. Vorteil dieser Variante ist, dass der komplexe EEBUS‑Stack komplett im Go‑Dienst gekapselt bleibt; die HA‑Seite bleibt rein MQTT‑basiert und damit einfach wartbar und portabel.[^10][^7][^8]

## Option 2: Externer Go‑Dienst mit gRPC/HTTP‑API und schlanker HA‑Custom‑Integration

In der Home‑Assistant‑Community wurde vorgeschlagen, einen EEBUS‑Dienst in Go bereitzustellen, der seine Funktionalität über eine RPC‑Schnittstelle (z.B. gRPC) bereitstellt, statt direkt MQTT zu sprechen. Die enbility‑Organisation pflegt neben eebus‑go auch ein Projekt eebus‑grpc sowie zusätzliche Bibliotheken wie ship‑go und spine‑go, die auf einen solchen Ansatz zugeschnitten sind.[^11][^7][^8]

In dieser Architektur:

- übernimmt ein EEBUS‑Backend (Go‑Dienst) alle Aufgaben rund um Discovery, Pairing, Zertifikatsverwaltung und SPINE‑Use‑Case‑Mapping und stellt eine klare, geräte‑ und use‑case‑orientierte API bereit (z.B. „Liste aller Geräte“, „Lies aktuelle elektrische Leistung der Wärmepumpe“, „Setze LPC‑Limit“, „Ändere Betriebsmodus“).
- Eine Home‑Assistant‑Custom‑Integration in Python redet ausschließlich mit diesem Backend (z.B. über gRPC, REST oder WebSocket), implementiert Config‑Flow, Optionen und Entity‑Mapping und muss selbst keine EEBUS‑Details kennen.

Vorteile:

- Ein generischer EEBUS‑Dienst kann von mehreren Frontends genutzt werden (Home Assistant, andere HEMS, eigene Skripte), was Wartung und Weiterentwicklung abstrahiert.[^8][^11]
- HA‑spezifische Logik (Konfiguration, Optionen, Entity‑Modelle) bleibt in Python, während der komplexe Protokoll‑Stack in Go mit eebus‑go, ship‑go und spine‑go realisiert wird.[^1][^8]

Nachteile sind zusätzlicher Betriebsaufwand (separater Dienst, Zertifikats‑Dateien, Monitoring) und komplexere Fehlersuche, da Fehler sich über zwei Komponenten (Backend und HA‑Integration) erstrecken können.[^7]

## Option 3: Native Python‑Integration auf Basis eines EEBUS‑Python‑Stacks

In den Home‑Assistant‑Diskussionen wird betont, dass ein fehlender Python‑EEBUS‑Stack der Hauptgrund dafür ist, dass es bisher keine generische EEBUS‑Integration gibt. Sollte ein Projekt wie das vom Nutzer genannte `py-eebus` oder ein anderes Python‑EEBUS‑SDK mittelfristig eine stabile Implementierung von SHIP und SPINE liefern, wäre ein „klassischer“ HA‑Ansatz möglich, bei dem:[^7]

- eine asynchrone Python‑Bibliothek EEBUS‑Sessions, Zertifikate, mDNS‑Discovery und Use‑Case‑Mapping kapselt,
- ein Home‑Assistant‑Custom‑Component direkt darauf aufsetzt und die Wärmepumpe als Gerät mit zugehörigen Entitäten modelliert.

Strukturell sähe das ähnlich aus wie bei anderen IP‑basierten Integrationen: `config_flow.py` für das Onboarding (IP oder mDNS‑Auswahl, SKI‑Vertrauensdialog), ein zentraler `coordinator` für die EEBUS‑Session und die Verteilung der Messwerte, sowie Entities, die spezifische SPINE‑Features der Vaillant‑Wärmepumpe repräsentieren (Heizen, Warmwasser, Messwerte, Fehlerzustände). Mangels einer heute nachweisbar reifen Python‑EEBUS‑Lib wäre dieser Ansatz aktuell aber mit hohem Entwicklungsaufwand und Protokoll‑Know‑how verbunden.[^5][^7]

## Option 4: Nutzung bestehender EEBUS‑EMS als Gateway (gridBox, SMA HEM, §14a‑Lösungen)

Verschiedene Hersteller setzen bereits auf EEBUS‑basierte Energiemanagement‑Systeme: Beispiele sind die SMA Home Energy Manager 2.0 sowie Lösungen von Anbietern wie gridX, die Vaillant‑Wärmepumpen über EEBUS‑Use‑Cases in ihre EMS‑Plattform einbinden. Die Einbindung einer Vaillant‑Wärmepumpe in eine gridBox erfolgt beispielsweise, indem zuerst das Vaillant‑Gateway (VR920/921/940f) mit dem LAN verbunden, dann in der myVaillant‑App der EEBUS‑Modus aktiviert und schließlich die gridBox anhand der SKI gekoppelt wird; danach erscheint die Wärmepumpe als Verbraucher im EMS.[^4][^12][^13][^14][^1]

Eine Home‑Assistant‑Integration könnte in diesem Szenario nicht direkt mit der Wärmepumpe sprechen, sondern die API des EMS nutzen (z.B. REST‑API, MQTT‑Feed oder Modbus‑TCP), um Messwerte und ggf. Steuerbefehle weiterzureichen. Vorteil: EEBUS‑Details, Pairing, Zertifikate und zukünftige Protokollerweiterungen liegen vollständig beim EMS‑Hersteller, während Home Assistant lediglich gegen eine dokumentierte API integriert wird; Nachteil ist Abhängigkeit von einem proprietären EMS, zusätzlichen Kosten und oft eingeschränkter Steuerbarkeit (z.B. nur indirekte Einflussnahme über Ziel‑Eigenverbrauch oder Limitwerte).[^12][^13][^4]

## Option 5: Indirekte Integration über bestehende eBUS/ebusd‑Pfad

Unabhängig von EEBUS bleibt der klassische Weg über eBUS und ebusd relevant, insbesondere wenn EEBUS‑Firmware oder ‑Gateway (noch) fehlen. Erfahrungsberichte zeigen, dass Vaillant aroTHERM‑Anlagen über einen eBUS‑Adapter (z.B. nach Referenzdesigns der ebus‑Community) mit einem Raspberry Pi und ebusd verbunden werden können, worüber sich viele Heizungs‑Parameter auslesen und teilweise schreiben lassen.[^2][^15][^10]

Home Assistant bietet bereits eine offizielle ebusd‑Integration sowie dokumentierte Wege, ebusd‑Messwerte per MQTT zu übernehmen, was von Anwendern für Vaillant‑Systeme genutzt wird. Dieser Ansatz ist technisch ausgereift, benötigt jedoch spezialisierte eBUS‑Hardware, basiert auf teilweise reverse‑engineerten Konfigurationsdateien und bietet keine standardisierten Use‑Cases wie EEBUS‑LPC oder herstellerübergreifende Interoperabilität.[^9][^10][^2][^5]

## Option 6: Generische EEBUS‑Integration in Home‑Assistant‑Core

Langfristig wäre eine generische EEBUS‑Integration in Home‑Assistant‑Core denkbar, wie sie in Feature‑Requests gefordert wird. Eine solche Integration müsste:[^16][^17][^7]

- EEBUS‑Geräte über mDNS/DNS‑SD (SHIP Discovery) erkennen und eine pairing‑/Zertifikatsstrecke (z.B. SKI‑Vertrauen, PIN‑Dialog) im Config‑Flow abbilden,[^5][^1]
- SPINE‑Use‑Cases auf Home‑Assistant‑Konzepte mappen (z.B. „heat pump“ mit elektrischer Leistung, thermischer Leistung, Temperaturen, Betriebsmodi; „EVSE“, „Battery“, „PV‑Inverter“),
- eine modulare, erweiterbare Use‑Case‑Schicht bieten, sodass neue EEBUS‑Spezifikationen ohne Umbau des Core integriert werden können.

Ohne einen stabilen Python‑EEBUS‑Stack wäre dies aber entweder mit erheblicher Eigenentwicklung verbunden oder würde faktisch auf eine Brücken‑Architektur wie in Option 2 hinauslaufen, bei der ein externer EEBUS‑Dienst genutzt wird und Home Assistant nur noch als Client fungiert. Aufgrund des Umfangs von SHIP und SPINE sowie der Vielzahl an Use‑Cases ist realistischerweise mit einem schrittweisen Ausbau (erst grundlegende Messwerte, später komplexe Lastmanagement‑Funktionen) zu rechnen.[^11][^1][^5][^7]

## Option 7: Nutzung bestehender Go‑Implementierungen aus EV‑Umfeld als Referenz

Neben eebus‑go existieren weitere (teilweise archivierte) Go‑Implementierungen, die sich auf EV‑Ladeanwendungsfälle konzentrieren, etwa das von evcc genutzte EEBUS‑Projekt, das Teile von EEBUS 1.0.1 für EV‑spezifische Use‑Cases implementiert. Diskussionen in diesem Umfeld zeigen, dass EEBUS bereits produktiv mit Wallboxen und teils auch mit Wärmepumpen kombiniert wird, etwa zur Umsetzung von Lastreduzierung nach §14a EnWG bei Vaillant‑Geräten.[^18][^3]

Für eine Home‑Assistant‑Integration können diese Projekte als Referenz dienen, wie man EEBUS‑Use‑Cases modelliert, Zertifikats‑Handling organisiert und SKI‑basierte Verbindungen robust implementiert; sie sind aber in der Regel nicht direkt als Bibliothek für allgemeine Wärmepumpensteuerung geeignet und müssen für diesen Zweck erweitert oder umgebaut werden.[^18][^3]

## Vergleich der Ansätze

| Ansatz | Protokoll‑Stack | Kopplung zu HA | Hauptvorteile | Hauptnachteile |
|-------|-----------------|----------------|---------------|----------------|
| Externer Go‑Dienst + MQTT | eebus‑go in Go | MQTT + MQTT Discovery | Klare Trennung (EEBUS ↔ MQTT), nutzt stabilen Go‑Stack, einfache HA‑Konfiguration | zusätzlicher Dienst, Mapping von Use‑Cases auf Topics selbst zu definieren |
| Externer Go‑Dienst + gRPC/HTTP | eebus‑go/eebus‑grpc | Eigene HA‑Custom‑Integration | Saubere API, mehrfach verwendbares Backend, gute Kapselung | hoher Entwicklungsaufwand (Go + Python), komplexere Fehlersuche |
| Native Python‑Integration | Python‑EEBUS‑Lib (z.B. py‑eebus, wenn reif) | direkter HA‑Custom‑Component | Keine zusätzlichen Dienste, typisch für HA‑Ökosystem | aktuell kein etablierter Python‑Stack, hohe Protokoll‑Komplexität |
| Bestehendes EEBUS‑EMS als Gateway | proprietärer EMS‑Stack | EMS‑API (REST/MQTT/Modbus) | Kein EEBUS‑Know‑how nötig, Hersteller trägt Protokollrisiko | Kosten, Hersteller‑Lock‑in, oft eingeschränkte Steuerung |
| eBUS/ebusd‑Pfad | ebusd + eBUS | Offizielle HA‑ebusd‑Integration oder MQTT | Bewährt, gut dokumentiert, keine EEBUS‑Firmware nötig | kein standardisiertes Lastmanagement, eBUS‑Hardware nötig, teils reverse‑engineert |
| EEBUS im HA‑Core | eigener (oder externer) Stack | Core‑Integration | Einheitliche Behandlung aller EEBUS‑Geräte, langfristig elegant | großer Implementierungs‑ und Wartungsaufwand, starke Abhängigkeit von Protokoll‑Evolution |

## Praktische Empfehlung für eine Vaillant aroTHERM plus

Für eine konkrete Umsetzung mit einer Vaillant aroTHERM plus und bestehender Home‑Assistant‑Installation bietet sich derzeit ein inkrementelles Vorgehen an:

- Kurzfristig: Wenn bereits eBUS‑Hardware vorhanden ist oder leicht beschafft werden kann, lässt sich mit ebusd und der vorhandenen HA‑Integration relativ schnell ein stabiler Zugriff auf viele Heizungs‑Parameter erreichen, wie Praxisberichte zu Vaillant‑Anlagen zeigen.[^15][^10][^2][^9]
- Mittelfristig: Für §14a‑konforme Lastbegrenzung oder herstellerübergreifendes Energiemanagement ist der Aufbau eines dedizierten EEBUS‑Dienstes auf Basis von eebus‑go mit MQTT‑Bridge (Option 1) oder gRPC‑Backend (Option 2) ein zukunftssicherer Weg, da eebus‑go aktiv gepflegt wird und ausdrücklich Use‑Cases für Wärmepumpen und EMS adressiert.[^3][^8][^1]
- Langfristig: Eine generische, eventuell in Home‑Assistant‑Core integrierte EEBUS‑Unterstützung hängt von der Verfügbarkeit robuster Python‑Stacks und Community‑Ressourcen ab und dürfte nur schrittweise entstehen; beim Design heutiger Brücken‑Dienste lohnt es, deren APIs so zu gestalten, dass sie später leicht in eine Core‑Integration überführt oder von dieser ersetzt werden können.[^11][^7]

---

## References

1. [Open Source EEBUS libraries released - enbility](https://enbility.net/blog/20221205-introduction/) - This library provides a complete foundation for implementing EEBUS use cases. The use cases define v...

2. [eBus und Vaillant Arotherm](https://romal.de/2023/11/16/ebus-und-vaillant-arotherm/)

3. [Vaillant: Dimmen gemäß § 14a EnWG via Relais und EEBUS LPC · evcc-io evcc · Discussion #19662](https://github.com/evcc-io/evcc/discussions/19662) - Ich habe mich in letzter Zeit damit beschäftigt, wie ich meine Vaillant aroTHERM plus VWL 75/6 in Zu...

4. [Vaillant Wärmepumpe via Internetmodul für EEBus Use ...](https://support.gridx.de/hc/de/articles/29036980739602-Vaillant-W%C3%A4rmepumpe-via-Internetmodul-f%C3%BCr-EEBus-Use-Cases-Inbetriebnahme) - Benötigt: myVaillant App Vaillant Gateway VR920, VR921, or VR940f Vaillant Systemsteuerungen multiMA...

5. [EEBUS](https://en.wikipedia.org/wiki/EEBUS)

6. [PP-PM-D-EEBUS-Schnittstelle-HEMS-240425-Final-FGI. ...](https://pressroom-rbt.com/rbt/uploads/2024/04/PP-PM-D-EEBUS-Schnittstelle-HEMS-240425-Final-FGI.docx)

7. [Support for EEBUS / SHIP / SPINE protocols - Feature Requests](https://community.home-assistant.io/t/support-for-eebus-ship-spine-protocols/352381) - It would be very useful if HomeAssistant could “talk” to EEBUS capable devices (e.g. Vaillant Heat P...

8. [enbility](https://github.com/enbility) - Solutions for using the EEBUS protocol. enbility has 9 repositories available. Follow their code on ...

9. [ebusd - Home Assistant](https://www.home-assistant.io/integrations/ebusd/) - The ebusd integration allows the integration between eBUS heating system and Home Assistant ... The ...

10. [Connected Vaillant to Home Assistant - Floris Romeijn](https://fromeijn.nl/connected-vaillant-to-home-assistant/) - Home Assistant Integration. To integrate with Home Assistant I used ebusd running on an Raspberry Pi...

11. [enbility](https://enbility.net) - EEBUS as Open Source Enable your products and services to support the energy management protocol EEB...

12. [Wärmepumpe Vaillant arotherm via EEBUS mit SMA SHM 2.0 koppeln - alle Ertragswerte im Sunny Portal](https://www.youtube.com/watch?v=beUTWIJQPWk) - Ganz perfekt ist es nicht: In diesem Video zeigen wir, wie unsere Wärmepumpe mit dem Sunny Home Mana...

13. [How does EEBUS work?](https://www.gridx.ai/knowledge/eebus-universal-communication-protocol-of-the-energy-world) - EEBUS protocol is an open-source standard that facilitates seamless communication between energy dev...

14. [EEBUS: Universelles Kommunikationsprotokoll der Energiewelt](https://www.gridx.ai/de/knowledge/eebus-universelles-kommunikationsprotokoll-der-energiewelt) - Das EEBUS-Protokoll ist ein Open-Source-Standard, der eine nahtlose Kommunikation zwischen Energiege...

15. [Installation](https://pyebus.readthedocs.io/en/stable/index.html)

16. [Wolf heat pump CHA and FHA EEBus integration](https://community.home-assistant.io/t/wolf-heat-pump-cha-and-fha-eebus-integration/936645) - ... Home Assistant and what can be done ... I have a Wolf CHA 7 as well and would be happy to see an...

17. [Eebus Energy manager P1 - #13 by BeyondPixels - Feature Requests](https://community.home-assistant.io/t/eebus-energy-manager-p1/302686/13) - Home Assistant Community · Eebus Energy manager P1 · Feature Requests ... As for PV forecast, there ...

18. [GitHub - evcc-io/eebus: (Partial) Implementation of the EEBUS protocol in Go](https://github.com/evcc-io/eebus) - (Partial) Implementation of the EEBUS protocol in Go - evcc-io/eebus

