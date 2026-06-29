<img src="https://r2cdn.perplexity.ai/pplx-full-logo-primary-dark%402x.png" style="height:64px;margin-right:32px"/>

# Recherchiere alle technischen Details, die zum Vaillant Energie Plus Dienst verfügbar sind und Vergleiche zum Funktionsumfang der Vaillant EEBUS-Implementierung. Ich suche nach Ansatzpunkten um meine rudimentäre EEBUS Ansteuerung einer Vaillant arotherm plus + uniTower über vr940f zu verbessern.

EnergiePLUS ist ein Vaillant‑Clouddienst, der Betrieb und Zeitpunkte deiner Wärmepumpe anhand PV‑Prognose und Börsenstrompreisen optimiert, während die EEBUS‑Implementierung über VR940f dir einen generischen Energiemanagement‑Kanal für Warmwasser, Heizpuffer und Leistungsbegrenzung bereitstellt, den du mit einem eigenen EEBUS‑Controller ausnutzen kannst.[^1_1][^1_2][^1_3]
Für eine eigene, bessere Ansteuerung musst du dich an den in der EEBUS‑Betriebsanleitung beschriebenen Funktionsumfang halten und die EnergiePLUS‑Logik (PV‑Vorhersage, Tarifoptimierung) lokal nachbilden.[^1_4][^1_5][^1_3]

***

## Ausgangslage: aroTHERM plus + VR940f

Deine aroTHERM plus mit uniTower ist laut GridX‑Doku und TEKKO‑Liste vollständig EEBUS‑fähig, wenn ein Systemregler wie multiMATIC VRC700 oder sensoCOMFORT VRC720 und ein Gateway VR920/921/940f verbaut sind.[^1_6][^1_7]
VR940f („myVAILLANT connect“) stellt die Verbindung zwischen eBUS‑Heizsystem und Internet her und ist explizit als Schnittstelle zu Smart‑Home‑ und Energiemanagement‑Systemen über EEBUS vorgesehen.[^1_8][^1_9]
Die Inbetriebnahme, Aktivierung von EEBUS und Kopplung externer EEBUS‑Partner erfolgt komplett über die myVaillant‑App (Menü Netzwerk‑Einstellungen → EEBUS, SKI‑Abgleich, „Vertrauen“).[^1_7][^1_3]

***

## EnergiePLUS: Architektur und Funktionsumfang

**Dienstart \& Zugriff**

- EnergiePLUS ist als eigenständiger Web‑Service unter energymanagement.myvaillant.com verfügbar und wird über deinen myVaillant‑Account mit der Wärmepumpe verknüpft.[^1_10][^1_5]
- Voraussetzung sind eine kompatible Vaillant‑Wärmepumpe, ein Internetmodul VR920/921/940 und ein aktiviertes myVaillant‑Konto; die Nutzung ist derzeit kostenlos, kann aber später kostenpflichtig werden.[^1_11][^1_5]

**Optimierungsmodi**

- Es gibt zwei getrennte Anwendungsfälle: PV‑Optimierung (Eigenverbrauchsmaximierung) und dynamische Tarif‑Optimierung; momentan musst du dich für einen Modus entscheiden, zukünftig ist eine Kombination geplant.[^1_2][^1_1][^1_11]
- Die PV‑Optimierung lädt vor allem den Warmwasserspeicher – und bei vorhandenem Heizpufferspeicher auch den Heizpuffer – bevorzugt dann, wenn die Standort‑PV‑Prognose einen Überschuss erwartet, und verschiebt dafür Ladezyklen in sonnenreiche Zeiten.[^1_1][^1_2]
- Die Tarif‑Optimierung nutzt Day‑Ahead‑Preisdaten der Strombörse EPEX Spot, identifiziert automatisch Niedrigpreis‑Zeitfenster und verschiebt Warmwasser‑ und ggf. Heizpufferladung in diese günstigen Stunden.[^1_5][^1_11][^1_2]

**Algorithmen \& Datenbasis**

- Laut AGB berechnet Vaillant Betriebszeiten und Solltemperaturen anhand Geo‑Daten, Wetterprognosen sowie Börsenstrompreisen und sendet die resultierenden Steuerbefehle über dein Internetmodul an die Wärmepumpe.[^1_5]
- Es wird ausdrücklich keine garantierte Einsparung zugesagt, da Wetter, Strompreise und Nutzungsverhalten variieren; angezeigte „Ersparnisse“ sind lediglich unverbindliche Berechnungen.[^1_5]

**Komfort \& Grenzen**

- EnergiePLUS verändert deine ursprünglichen Zeitprogramme in der App nicht dauerhaft; es darf die Wärmepumpe aber auch außerhalb dieser Zeitfenster einschalten, wenn Optimierungspotenzial besteht, und Warmwassertemperaturen temporär erhöhen.[^1_11]
- Für Heizbetriebs‑Optimierung ist ein Heizungspufferspeicher erforderlich, sonst kann der Heizbetrieb nicht gezielt verschoben werden.[^1_11]
- Kaskaden‑Wärmepumpen und Batteriespeicher werden derzeit nicht unterstützt; Hybridsysteme sind möglich, aber mit reduziertem Funktionsumfang.[^1_2][^1_11]

**Exklusivität gegenüber anderen Energiemanagern**

- Vaillant sagt explizit, dass parallel zu EnergiePLUS kein weiteres Energiemanagement‑System (Smart‑Home‑ oder EEBUS‑basiert) an die Heizung angebunden sein darf, da sich mehrere Systeme gegenseitig stören könnten.[^1_11]
- Nur wenn dein System über die Vaillant‑API durch eine Dritt‑Firma gesteuert wird, ist eine gleichzeitige Nutzung toleriert, empfohlen wird aber die Konzentration auf ein System.[^1_11]

***

## Vaillant‑EEBUS‑Implementierung über VR940f

**Systemaufbau**

Die EEBUS‑Betriebsanleitung beschreibt den generischen Aufbau des Vaillant‑Systems:[^1_3]

- Komponenten: Vaillant‑Heizsystem (inkl. Wärmepumpe), Systemregler VRC700/720, Internetgateway VR920/921/940f, myVaillant‑App, Vaillant‑Cloud, Router sowie mindestens eine zusätzliche EEBUS‑fähige Komponente (Energiemanager).[^1_3]
- Das Internetgateway bildet die Brücke zur Vaillant‑Cloud und stellt gleichzeitig die lokale EEBUS‑Kommunikation mit externen Energiemanagern und Smart‑Home‑Systemen her.[^1_9][^1_3]

**EEBUS‑Anwendungsfälle (Use‑Cases)**

Die dokumentierten Use‑Cases der Vaillant‑EEBUS‑Implementierung sind:[^1_3]

- Energiemanagement von Wärmepumpen (Warmwasser und Heizung) über Nutzung thermischer Speicher (WW‑Speicher, Heizpuffer).[^1_3]
- Begrenzung der elektrischen Leistung der Wärmepumpe durch externe Energiemanager oder Netzbetreiber (relevant für §14a‑Szenarien).[^1_3]
- Transparenzfunktionen: Anzeige von PV‑Arbeit über EEBUS und Bereitstellung der aktuellen elektrischen Leistungsaufnahme der Wärmepumpe.[^1_3]
- Einstellung und Anzeige von Betriebsarten und Solltemperaturen für Warmwasser‑ und Heizkreise über externe EEBUS‑Clients (z. B. Energiemanagement‑Controller).[^1_3]

**Konkrete Energiemanagement‑Logik im Gerät**

Die Anleitung unterscheidet explizit zwischen interner Regelung und externem Energiemanager:[^1_3]

- Bei aktivem Energiemanagement „Warmwasser“ nutzt die Wärmepumpe externe Aktivierungssignale (z. B. „PV‑Überschuss verfügbar“), um den Speicher über die normale Solltemperatur hinaus bis zu einer konfigurierten Maximaltemperatur zu laden, sowohl innerhalb als auch außerhalb der regulären Zeitfenster.[^1_3]
- Beim Energiemanagement „Heizung“ wird der Heizpuffer bei Aktivierung über die Solltemperatur bis zu einer Offset‑Temperatur aufgeheizt, um thermische Energie zu speichern; dazu müssen ein VRC700/720 und ein konfigurierter Heizpufferspeicher vorhanden sein.[^1_3]
- Neuere Geräte (ab Baujahr 2023) wie aroTHERM plus unterstützen ein EEBUS‑basiertes Limitieren der elektrischen Wärmepumpenleistung; der Netzbetreiber oder Energiemanager kann so die Leistungsaufnahme in kritischen Netzsituationen drosseln.[^1_3]

**Einschränkungen und Kompatibilität**

- AmbiSENSE‑Raumregelung ist nicht mit der EEBUS‑Funktionalität kombinierbar; bestimmte Systemkonfigurationen (z. B. externe elektrische Zusatzheizung über VWZ AI) schließen die Leistungsbegrenzungsfunktion ebenfalls aus.[^1_3]
- TEKKO bestätigt, dass die Kühlfunktion bei Vaillant‑Wärmepumpen Stand 2022 nicht über EEBUS steuerbar ist; Heiz/Kühlumschaltung muss lokal oder über externe Kontakte erfolgen.[^1_6]
- myVaillant‑App dient zur Aktivierung von EEBUS, Kopplung externer EEBUS‑Partner (SKI‑Vergleich, Trust‑Beziehung) und Konfiguration der Energiemanagement‑Parameter.[^1_7][^1_3]

***

## EnergiePLUS vs. EEBUS: funktionaler Vergleich

| Aspekt | EnergiePLUS | Vaillant EEBUS über VR940f |
| :-- | :-- | :-- |
| Ort der Intelligenz | Vaillant‑Cloud (Webdienst) berechnet Zeitpläne und Temperaturen.[^1_5][^1_2] | Externer EEBUS‑Energiemanager (z. B. TEKKO, GridX, eigener Dienst) steuert, Wärmepumpe setzt nur generische Kommandos um.[^1_3][^1_6] |
| Optimierungsziele | PV‑Eigenverbrauch, dynamische Strompreise (EPEX Spot Day‑Ahead).[^1_2][^1_5][^1_11] | Beliebig (PV, Tarife, Netzrestriktionen, Lastmanagement), abhängig von deiner eigenen Logik.[^1_3] |
| Datenbasis | Geo‑Daten, Wetterprognosen, PV‑Prognosen, Börsenstrompreise; alles vom Vaillant‑Dienst bereitgestellt.[^1_2][^1_5] | Was dein eigenes Energiemanagement beisteuert (z. B. PV‑Messwerte, Forecasts, Tarife); Vaillant stellt nur interne Zustände und wenige Messgrößen bereit.[^1_3] |
| Steuergrößen | Automatische Anpassung von Betriebszeiten und Zieltemperaturen für Warmwasser/Heizpuffer innerhalb Komfortgrenzen.[^1_1][^1_2] | Aktivieren/Deaktivieren des Energiemanagements, Setzen von Max‑Temp/Offset, Leistungsgrenzen, Betriebsmodus‑ und Sollwert‑Änderungen via EEBUS‑Telegramme.[^1_3] |
| Schnittstelle | Weboberfläche (EnergiePLUS‑Dashboard) + myVaillant‑Link, keine offene API für Drittlogik.[^1_10][^1_11][^1_5] | Standard‑EEBUS/SHIP‑Schnittstelle über VR940f; vollständig von eigenem EEBUS‑Client nutzbar.[^1_3][^1_9] |
| Parallelbetrieb | Exklusiv: keine gleichzeitige Verbindung eines weiteren Energiemanagement‑Systems (EEBUS/Smart‑Home) erlaubt.[^1_11] | Kann als einziger Energiemanager angebunden werden; bei Nutzung von EnergiePLUS sollst du EEBUS‑Energiemanagement deaktivieren.[^1_11][^1_3] |

Faktisch nutzt Vaillant für EnergiePLUS dieselben physischen Komponenten (Wärmepumpe, Systemregler, VR940f, Cloud) wie bei EEBUS‑Energiemanagement, nur dass die Optimierungslogik in der Vaillant‑Cloud statt in deinem eigenen Controller läuft.[^1_4][^1_2][^1_3]

***

## Ansatzpunkte zur Verbesserung deiner EEBUS‑Ansteuerung

### 1. Klarer Fokus: Entweder EnergiePLUS oder eigener Energiemanager

Da Vaillant parallele EEBUS‑Energiemanager und EnergiePLUS explizit ausschließt, solltest du für eine eigene Optimierung EnergiePLUS deaktivieren und VR940f ausschließlich mit deinem EEBUS‑Dienst koppeln.[^1_11][^1_3]
Damit vermeidest du widersprüchliche Steuerkommandos und hast die volle Kontrolle über Energiemanagement‑Signale und §14a‑Anwendungen.[^1_3]

### 2. EEBUS‑Use‑Cases gezielt ausnutzen

Aus der Betriebsanleitung lassen sich drei praxisnahe Steuerkanäle ableiten, an denen du mit py‑eebus oder eebus‑go ansetzen kannst:[^1_6][^1_3]

- **Warmwasser‑Energiemanagement**
Nutze die EEBUS‑Funktion „Energiemanagement Warmwasser“, um bei PV‑Überschuss oder niedrigen Tarifen einen Boost oberhalb der Standard‑Solltemperatur zu initiieren und so mehr thermische Energie im uniTower zu speichern.[^1_4][^1_3]
Deine Logik bestimmt, wann du diesen Boost auslöst (z. B. wenn PV‑Prognose > X kW für Y Stunden oder Strompreis < Schwelle), Vaillant kümmert sich um die sichere Umsetzung und Einhaltung der Max‑Temperatur.[^1_2][^1_5][^1_3]
- **Heizpuffer‑Energiemanagement**
Falls du einen Heizpufferspeicher hast, kannst du die Funktion „Energiemanagement Heizung“ nutzen, um den Puffer gezielt zu überladen, wenn PV‑Überschuss oder günstige Tarife vorliegen, und in teuren Zeiten die Heizleistung zu reduzieren.[^1_11][^1_3]
Praktisch entspricht das dem EnergiePLUS‑Verhalten, nur dass du die Auslösekriterien selbst definierst und z. B. auch Hauslast, EV‑Laden und §14a‑Limits einbeziehst.[^1_2]
- **Leistungsbegrenzung der Wärmepumpe**
Für aroTHERM plus ab Baujahr 2023 kannst du über EEBUS eine maximale elektrische Leistung vorgeben; das ist Vaillants technische Basis für Netzbetreiber‑Steuerung nach §14a EnWG.[^1_12][^1_3]
Du kannst diese Funktion in deinem Energiemanager nutzen, um Lastspitzen zu glätten, EV‑Laden und Wärmepumpe zu koordinieren oder manuell verschärfte Limits zu setzen, wenn du z. B. in einem schwachen Netzsegment wohnst.[^1_3]


### 3. Lokale Replikation der EnergiePLUS‑Algorithmen

EnergiePLUS selbst ist nicht dokumentiert, aber aus Beschreibung und AGB kannst du seine Logik recht gut nachbilden:[^1_1][^1_2][^1_5]

- **PV‑Modus nachbauen**
Hole dir eine Standort‑Wetterprognose (z. B. über einen HA‑Sensor) und kombiniere sie mit einer PV‑Ertragsprognose aus deiner PV‑Anlage oder einem Modell.[^1_1][^1_2]
Wenn für ein kommendes Zeitfenster gleichzeitig hoher PV‑Ertrag und freie Speicherkapazität (Warmwasser/Heizpuffer) vorliegen, sende über EEBUS ein „Energiemanagement aktiv“ plus erhöhte Max‑Temperaturen und lass Vaillant die Speicher laden.[^1_4][^1_3]
- **Tarif‑Modus nachbauen**
Importiere dynamische Preise (Tibber, Awattar, …) in Home Assistant und berechne eine 24‑Stunden‑Optimierung, bei der Warmwasser‑Ladezeiten in die günstigsten Stunden gelegt werden, analog zur EnergiePLUS‑Beschreibung.[^1_5][^1_11]
Deine EEBUS‑Logik löst in diesen Stunden Warmwasser‑Boosts aus und senkt in teuren Stunden ggf. die Max‑Temperatur oder schaltet Energiemanagement ab.[^1_11][^1_3]
- **Komfortgrenzen respektieren**
Vaillant betont, dass Komfortgrenzen und Temperaturbegrenzungen immer eingehalten werden; dein Energiemanager sollte also Max‑Temperatur‑Parameter nur in sinnvollen Grenzen verändern und Legionellenzyklen, Mindesttemperaturen etc. unangetastet lassen.[^1_5][^1_11][^1_3]


### 4. Technische Umsetzung mit py‑eebus / eebus‑go

Die Betriebsanleitung zeigt, dass VR940f nur die generischen EEBUS‑Use‑Cases implementiert; konkrete Datenpunkte (SHIP‑Function‑Sets) musst du mit einem generischen EEBUS‑Stack auslesen.[^1_13][^1_3]

Praktisches Vorgehen:

- Setze einen separaten Container mit py‑eebus oder eebus‑go auf, der als „Energy Manager“ gegenüber VR940f auftritt und die in der Anleitung beschriebenen Energiemanagement‑Use‑Cases nutzt.[^1_3]
- Kopple diesen Dienst über MQTT oder HTTP mit Home Assistant, sodass HA deine PV‑, Tarif‑ und Lastdaten liefert und deine Automationen wiederum EEBUS‑Kommandos an den Wärmepumpen‑Energiemanager schicken.[^1_9][^1_7]
- Logge und analysiere die Reaktion der Anlage auf unterschiedliche Energiemanagement‑Signale (Warmwasser/Heizpuffer/Leistungslimit), um die wirksamsten Stellgrößen zu identifizieren und deine Regeln iterativ zu schärfen.[^1_6][^1_3]


### 5. Grenzen der Steuerbarkeit akzeptieren

Die Dokumentation macht klar, dass Vaillant über EEBUS **keine vollständige Direktsteuerung des Kompressors** anbietet, sondern nur über die beschriebenen Energiemanagement‑Use‑Cases und Leistungsgrenzen beeinflussbar ist.[^1_3]
Feinheiten wie interne Heizkurven, Frostschutz, Legionellenschutz und viele Details der Warmwasser‑Hydraulik bleiben in der Hoheit der Vaillant‑Regler und der myVaillant‑Konfiguration; eine „vollständige“ Ersatzimplementierung von EnergiePLUS wird daher immer an diese Grenzen stoßen.[^1_4][^1_3]

***

Wenn du möchtest, können wir im nächsten Schritt auf Basis deiner aktuellen py‑eebus/eebus‑go‑Implementierung ein konkretes Topic‑/API‑Schema für Warmwasser‑Boost, Heizpuffer‑Ladung und Leistungsbegrenzung skizzieren, das möglichst nah an EnergiePLUS angelehnt ist und gleichzeitig deine §14a‑ und EV‑Use‑Cases berücksichtigt.
<span style="display:none">[^1_14][^1_15][^1_16][^1_17][^1_18][^1_19][^1_20][^1_21][^1_22][^1_23][^1_24][^1_25][^1_26][^1_27][^1_28]</span>

<div align="center">⁂</div>

[^1_1]: https://www.vaillant.de/service/energiemanagement/

[^1_2]: https://www.vaillant.at/unternehmen/presse-information/produktentwicklung/energieplus/

[^1_3]: https://www.vaillant.de/vaillant-de/2-service/mobile-apps/myvaillant-app/eebusmanual-3018678.pdf

[^1_4]: https://www.vaillant.de/ratgeber/heizung-im-smart-home/smarte-waermepumpe/

[^1_5]: https://energymanagement.myvaillant.com/files/login/AGB_2025_06.pdf

[^1_6]: https://www.tekko-ga.com/wiki/index/gerate/vaillant/handbuchvail/allgemeines_22/kompatiblege_1/kompatiblege_1.html

[^1_7]: https://support.gridx.de/en/articles/640581-vaillant-arotherm-and-other-heat-pumps

[^1_8]: https://www.alles-mit-stecker.de/Installationsmaterial/Instabus-KNX-EIB/Systemschnittstelle-Medien-Gateway/Vaillant-eBUS-Schnittstelle-WLAN-Internetmodul-VR-940f-myVAILLANT::1350069.html

[^1_9]: https://www.vaillant.at/privatanwender/produkte/internetkommunikationsmodul-myvaillant-connect-vr940f-227968.html

[^1_10]: https://energymanagement.myvaillant.com

[^1_11]: https://vaillant.de/heizung/service/energiemanagement/

[^1_12]: https://www.heima24.de/heizung/vaillant-waermepumpenpaket-43408-arotherm-plus-vwl-5581-a-s2-mit-steuerungssystem-vwz-ai-internetmodul-vr-940f-8000044766.html

[^1_13]: https://www.manualslib.de/manual/151061/Vaillant-Eebus.html

[^1_14]: https://www.youtube.com/watch?v=uE5KFuDaHYI

[^1_15]: https://www.youtube.com/watch?v=UScTOQQQkkM

[^1_16]: https://www.vaillant.pl/podreczniki-schematow/pompy-ciepla/podrcznik-schematw-hybrydowych-arotherm-split-plus-03-04-25-3006201.pdf

[^1_17]: https://www.vaillant.de/service/myvaillant-portal/

[^1_18]: https://www.vaillant.at/privatanwender/service-support/digitales-energiemanagement/

[^1_19]: https://www.youtube.com/watch?v=rPCemOJ9PFs

[^1_20]: https://kontakt.vaillant.de/documents/download/321267/

[^1_21]: https://www.elektro4000.de/Industriebedarf/Installations-Bussysteme/Bussystem-Systemschnittstelle-Medien-Gateway/Vaillant-eBUS-Schnittstelle-VR-940f-myVAILLANT::1888143.html

[^1_22]: https://www.vaillant.de/documents/download/72202

[^1_23]: https://www.vaillant.hu/downloads/07-szabalyzok/vaillant-cc-pli-2023-2708018.pdf

[^1_24]: https://www.vaillant.at/downloads/installationsanleitungen/regelung-1/0020288139-00-deu-2054534.pdf

[^1_25]: https://www.vaillant.hu/downloads/07-szabalyzok/vr940f-myvaillant-connect-1/0020328017-02-3063857.pdf

[^1_26]: https://kontakt.vaillant.de/documents/download/131776/

[^1_27]: https://www.vaillant.ch/downloads/installationsanleitungen/deutsch/regelungstechnik/zusatzmodule/installationsanleitung-vr34-2123933.pdf

[^1_28]: https://www.vaillant.nl/producten-3/myvaillant-connect/installatiehandleiding-myvaillant-connect-vr-940f-0020328018-03-2649226.pdf


---

# Pv-Wechselrichter und Solarprognose mit Solcast-Integration habe ich in Home Assistant verfügbar. Die EPEX-Spot-Integration ist ebenfalls bereits eingebunden. Für die Lastoptimierung möchte ich die HACS-Integration HAEO nutzen, die ich noch nicht konfiguriert habe. Mein eigenes EEBUS-Modul findest du in https://github.com/volschin/eebus-ha-bridge. Mach einen Designentwurf, wie das ganze in HomeAssistant zusammenspielen könnte.

Zielbild: Du modellierst dein Hausenergiesystem vollständig in HAEO (Grid, PV, Wärmepumpe, ggf. EV), speist Solcast‑PV‑Forecast und EPEX‑Preise als Eingänge ein und lässt HAEO optimale Zeitpläne und Leistungsprofile berechnen; diese werden über Automationen in Home Assistant in EEBUS‑Kommandos übersetzt, die dein Vaillant‑System über die eebus‑ha‑bridge an VR940f weitergibt.[^2_1][^2_2][^2_3]

***

## Gesamtarchitektur

Gedanklich läuft es so:

1. **Sensor‑Ebene (HA)**
    - PV‑Ist‑Leistung + Solcast‑Forecast.[^2_4][^2_5][^2_6]
    - EPEX‑Spot‑Preissensor(en) inkl. ggf. Aufschläge/USt.[^2_7][^2_8][^2_9]
    - Vaillant‑Zustandssensoren aus der eebus‑ha‑bridge (WW‑Speicher, Heizpuffer, WP‑Leistung, Betriebszustände).[^2_3]
2. **Optimierungsebene (HAEO)**
    - Ein HAEO‑Hub modelliert dein Gesamtsystem: Grid, Solar, Load „Wärmepumpe“ (später EV/Batterie).[^2_10][^2_2]
    - HAEO liest Preis‑ und Forecast‑Sensoren, baut ein LP‑Modell und erzeugt für jedes Element Zeitreihen mit „Soll‑Leistung / Soll‑Energie“.[^2_2][^2_1]
3. **Bridging‑Ebene (Automationen + eebus‑ha‑bridge)**
    - Automationen beobachten die HAEO‑Outputs und rufen Dienste deiner eebus‑ha‑bridge auf, um EEBUS‑Energiemanagement (Warmwasser/Heizung) und Leistungslimits in der aroTHERM plus zu setzen.[^2_3]
    - Das Vaillant‑System setzt diese Kommandos um, nutzt thermische Speicher und interne Regelung zur tatsächlichen WP‑Führung.[^2_3]

So bleibt HAEO das „Gehirn“ für Kosten‑/Lastoptimierung, während Vaillant über EEBUS die physische Umsetzung übernimmt.

***

## Datenquellen in Home Assistant

### PV und Solcast

- Solcast‑Integration liefert Forecast‑Sensoren mit erwarteter PV‑Leistung/Energie pro Zeitslot; HACS‑Integrationen wie `ha-solcast-solar` hängen sich direkt ans Energy Dashboard und stellen Forecast‑Attribute bereit.[^2_6][^2_4]
- Zusätzlich nutzt du deinen Wechselrichter‑Sensor (Ist‑PV‑Leistung / Energie) für Feedback und ggf. für historische Forecasts (HAEO + HAFO).[^2_11][^2_12]


### EPEX Spot

- Deine EPEX‑Integration liefert pro Stunde Preise in €/kWh oder €/MWh; gängige Custom‑Components wie `ha_epex_spot` stellen Rohpreise samt Attributen für „raw_today / raw_tomorrow“ bereit, die sich gut für Optimierung eignen.[^2_8][^2_9]
- Typischer Pattern: ein „Nettopreis“-Sensor (nur Spotpreis) plus ein „Effektivpreis“-Sensor (Spot + Aufschlag + USt), den du direkt in HAEO als Grid‑Preisquelle nutzt.[^2_9]


### Vaillant / eebus‑ha‑bridge

Da dein Repo nicht gelesen werden konnte, gehe ich von einem typischen Design aus: du exposest EEBUS‑Informationen als HA‑Sensoren/Dienste.

Sinnvolle Entitäten/Dienste:

- Sensoren:
    - `sensor.vaillant_hp_power` (aktuelle WP‑Leistungsaufnahme)
    - `sensor.vaillant_dhw_temp`, `sensor.vaillant_buffer_temp` (WW‑/Puffer‑Temperatur)
    - `sensor.vaillant_hp_mode` (Heizen / WW / Standby)
- Dienste:
    - `eebus_ha_bridge.set_dhw_energy_management(enabled: bool, max_temp: float)`
    - `eebus_ha_bridge.set_buffer_energy_management(enabled: bool, offset_temp: float)`
    - `eebus_ha_bridge.set_hp_power_limit(limit_kw: float)`

Diese Dienste solltest du, falls noch nicht vorhanden, genau entlang der Vaillant‑EEBUS‑Use‑Cases aufbauen (WM‑Energiemanagement, Heizpuffer‑Energiemanagement, Leistungsbegrenzung).[^2_3]

***

## HAEO‑Modellierung deines Systems

### HAEO‑Hub

- Lege einen Hub „Home Energy System“ an und definiere Interval‑Tiers z. B. nahe am Default:
    - Tier 1: 5 × 1 min – hochauflösende Steuerung kurzfristig (WP‑Start/Stop, EV‑Laden).
    - Tier 2: 5 × 5 min – kurzfristige Lastanpassung.
    - Tier 3: 46 × 30 min – Day‑Ahead‑Optimierung.
    - Tier 4: 48 × 60 min – 2‑Tage‑Horizont.[^2_10]

Damit hat HAEO ein ca. 72‑h‑Horizont mit feiner Auflösung in den nächsten Minuten/Stunden.[^2_2][^2_10]

### Elemente

Empfohlene Elementstruktur:[^2_1][^2_10]

- **Grid‑Element**
    - Preis‑Input: EPEX‑Effektivpreis‑Sensor (Importkosten), optional zweiter Sensor für Exportvergütung (Einspeisevergütung).
    - Limits: max. Import‑Leistung (Hausanschluss), optional Export‑Limit (WR‑Begrenzung).
- **Solar‑Element**
    - Forecast‑Input: Solcast‑Forecast‑Sensor (kW oder kWh), der Forecast als `forecast`‑Attribut liefert.[^2_5][^2_12]
    - Ist‑Leistung: Wechselrichter‑Sensor, um Realitätscheck und ggf. Curtailment zu modellieren.
- **Load‑Element „Heat Pump“**
    - Baseline‑Last: historischer Verbrauch oder ein Load‑Forecast‑Sensor, erzeugt z. B. mit HAFO/HAFO‑ähnlichem Setup (Hauslast oder dedizierte WP‑Leistung).[^2_12]
    - Flexibilität: Felder für maximalen zusätzlichen Verbrauch (WW‑Boost/Pufferladung) und minimalen Verbrauch (legionellen‑ / Frostschutz‑Grenzen).
    - Optional: zwei Load‑Elemente, eines für Warmwasser, eines für Heizung, um unterschiedliche Flexibilität abzubilden.
- **Weitere Loads (später)**
    - EV‑Laden als Load (ggf. mit eigener Batterie‑Elementstruktur).
    - Haushalt „non‑shiftable“ Load als ConstantLoad / ForecastLoad.
- **Connections**
    - Node „House“ als zentraler Knoten, an den Grid, Solar, HP‑Load, EV‑Load und ggf. Batterie angeschlossen werden.[^2_10][^2_2]
    - Standardverbindungen reichen meist, explizite Connection‑Elemente nur wenn du spezielle Effizienzen/Kosten modellieren willst.

HAEO berechnet dann für jedes Intervall:

- Grid‑Import/Export‑Leistung.
- Solar‑Nutzung (direkter Verbrauch vs. Curtailment).
- HP‑Load‑Leistung (zusätzliche Ladung von Speicher vs. minimale Grundlast).

Diese Ergebnisse werden als Sensoren mit `forecast`‑Attributen publiziert.[^2_1][^2_2]

***

## Übersetzung HAEO → EEBUS über Automationen

### Kernidee

Du nimmst nicht jede einzelne Intervall‑Entscheidung als separate Automation, sondern baust wenige höher‑levelige Regeln, die HAEO‑Forecasts interpretieren und daraus EEBUS‑Kommandos ableiten.[^2_12][^2_2]

#### 1. Warmwasser‑Boost basierend auf PV/Preis‑Fenstern

Inputs:

- HAEO‑Sensor `sensor.haeo_heat_pump_extra_load_forecast` (zeigt Zeitfenster mit zusätzlicher WP‑Last).
- Solcast‑Forecast (PV‑Überschuss) und EPEX‑Preis als Backup‑Kontrolle.

Automation‑Logik (vereinfacht):

- Wenn in einem kommenden Zeitraum `t` der HAEO‑Load‑Forecast eine signifikante zusätzliche Last für „Heat Pump – DHW“ und gleichzeitig hohe PV oder niedriger Preis anzeigt, dann:
    - Rufe `eebus_ha_bridge.set_dhw_energy_management(enabled=true, max_temp=<komfortgrenze>)` für diesen Zeitraum auf.[^2_3]
- Nach Ablauf des Zeitfensters oder wenn die Real‑Conditions (z. B. kein PV‑Überschuss) stark abweichen, deaktiviere das Energiemanagement oder senke die Max‑Temperatur wieder.

Damit replizierst du die EnergiePLUS‑Warmwasser‑Logik lokal, aber parametrierst sie durch HAEO‑Optimierung.[^2_13][^2_14][^2_3]

#### 2. Heizpuffer‑Ladung / Vorwärmung

Wenn du einen Heizpufferspeicher hast:[^2_3]

- HAEO‑Load „Heat Pump – Heating“ und Solcast/EPEX geben vor, wann Vorwärmen sinnvoll ist.
- Automation prüft, ob `sensor.vaillant_buffer_temp` unter einer oberen Komfortgrenze liegt und ein Intervall mit günstigen Bedingungen kommt.
- Dann: `eebus_ha_bridge.set_buffer_energy_management(enabled=true, offset_temp=<z.B. +5 K>)`.
- In teuren Zeiten oder bei niedriger PV‑Prognose: Energiemanagement „Heizung“ wieder deaktivieren, WP arbeitet nur nach Standard‑Zeitprogramm.


#### 3. Leistungsbegrenzung nach Netz‑/Preisbedingungen (§14a‑ähnlich)

Inputs:

- HAEO‑Grid‑Sensor mit Importleistung und Kosten.
- Optional `sensor.house_total_power` zur Erkennung von Lastspitzen.

Automation:

- Wenn Gesamtlast > Schwellwert oder Preis extrem hoch:
    - `eebus_ha_bridge.set_hp_power_limit(limit_kw=X)` – X aus HAEO‑Optimierung oder statischer Regel.[^2_3]
- Wenn Last/Preis wieder normal:
    - Limit zurücknehmen oder auf „kein Limit“ setzen.

So kannst du dein eigenes „§14a Light“ realisieren, long‑term auch mit echten Netzbetreiber‑Signalen kombinierbar.[^2_3]

***

## Entitäts‑ und Automations‑Skizze in Home Assistant

### Wichtige Entities

- Solcast:
    - `sensor.solcast_pv_forecast_power` / `sensor.solcast_pv_forecast_energy`.[^2_4][^2_6]
- EPEX:
    - `sensor.epex_spot_raw_price` (Spot)
    - `sensor.epex_spot_effective_price` (Spot+Aufschlag+USt).[^2_8][^2_9]
- HAEO Output:
    - `sensor.haeo_grid_import_forecast`
    - `sensor.haeo_heat_pump_load_forecast` (ggf. getrennt: DHW/Heizung)
    - `sensor.haeo_network_cost_forecast`.[^2_10][^2_1]
- Vaillant / eebus‑ha‑bridge:
    - `sensor.vaillant_hp_power`, `sensor.vaillant_dhw_temp`, `sensor.vaillant_buffer_temp`.
    - Dienste wie oben skizziert.


### Automations (auf hoher Ebene)

1. **„HP Warmwasser Boost planen“**
    - Trigger: Zeitmuster (alle 15–30 min) + Änderung in HAEO‑Forecast‑Sensoren.
    - Condition: Kommendes Zeitfenster mit günstiger Kombination aus `haeo_heat_pump_load_forecast`, PV‑Forecast und Preis.
    - Action: Dienst `set_dhw_energy_management` mit passenden Max‑Temperaturen.
2. **„Heizpuffer Vorwärmen“**
    - Trigger: Gleicher Ansatz, aber mit Heiz‑Load‑Forecast.
    - Condition: Buffer‑Temp < obere Grenze, PV‑Überschuss erwartet.
    - Action: Dienst `set_buffer_energy_management`.
3. **„HP Leistungslimit bei Lastspitzen“**
    - Trigger: `sensor.house_total_power` > Threshold oder HAEO‑Grid‑Import nahe Anschlussgrenze.
    - Action: Dienst `set_hp_power_limit(limit_kw=…)`.
4. **Fail‑Safe / Komfortschutz**
    - Trigger: Buffer/DHW‑Temp zu niedrig oder HP zu lange aus.
    - Action: deaktiviert Energiemanagement, setzt Temp‑Limits auf Standardwerte (Komfort gewinnt).

***

## Praktische Schritte zur Umsetzung

1. **HAEO sauber konfigurieren**
    - Hub mit sinnvoller Intervallkonfiguration.
    - Grid‑Element mit EPEX‑Effective‑Preis.
    - Solar‑Element mit Solcast‑Forecast und WR‑Ist.
    - Load‑Element(e) für WP‑Last mit einfachem Forecast (Hauslast oder HP‑Last)..[^2_12][^2_10]
2. **eebus‑ha‑bridge API schärfen**
    - Sicherstellen, dass Dienste für die drei EEBUS‑Use‑Cases existieren: WW‑Energiemanagement, Puffer‑Energiemanagement, Leistungsbegrenzung.[^2_3]
    - Dienste idempotent designen (gleiches Kommando mehrfach ohne Nebenwirkung).
3. **Automationen iterativ aufbauen**
    - Zuerst nur Warmwasser‑Boost mit PV‑Überschuss testen (ohne Preiskomponente) und die Reaktion der aroTHERM plus beobachten.[^2_3]
    - Dann EPEX‑Preise dazu nehmen, schließlich Leistungsbegrenzungslogik ergänzen.
4. **Monitoring \& Feinschliff**
    - Mit HAEO‑Netzwerksensoren die Optimierungskosten und Laufzeiten beobachten.[^2_2][^2_10]
    - In HA Grafana/Influx die Zeitreihen von HP‑Leistung, PV‑Ertrag und Preisen analysieren, um deine Schwellen/Heuristiken zu verfeinern.

***

Wenn du magst, kann ich im nächsten Schritt konkret YAML‑Templates für 1–2 Automationen skizzieren, z. B. „Warmwasser‑Boost bei PV‑Überschuss und niedrigen EPEX‑Preisen“, die direkt dein eebus‑ha‑bridge‑Service‑Interface nutzen.
<span style="display:none">[^2_15][^2_16][^2_17][^2_18][^2_19][^2_20][^2_21][^2_22][^2_23][^2_24][^2_25][^2_26][^2_27][^2_28][^2_29][^2_30][^2_31][^2_32][^2_33]</span>

<div align="center">⁂</div>

[^2_1]: https://haeo.io

[^2_2]: http://haeo.io/developer-guide/architecture/

[^2_3]: https://www.vaillant.de/vaillant-de/2-service/mobile-apps/myvaillant-app/eebusmanual-3018678.pdf

[^2_4]: https://github.com/BJReplay/ha-solcast-solar

[^2_5]: https://context7.com/bjreplay/ha-solcast-solar

[^2_6]: https://github.com/696GrocuttT/ha-solcast-solar

[^2_7]: https://community.home-assistant.io/t/populating-helper-entities-with-epex-spot-data/851883

[^2_8]: https://github.com/mampfes/ha_epex_spot

[^2_9]: https://community.home-assistant.io/t/epex-spot-and-awattar-electricity-prices/519151?page=3

[^2_10]: https://haeo.io/user-guide/configuration/

[^2_11]: https://totaldebug.uk/posts/solcast-api-home-assistant/

[^2_12]: https://haeo.io/user-guide/historical-load-forecast/

[^2_13]: https://www.vaillant.at/unternehmen/presse-information/produktentwicklung/energieplus/

[^2_14]: https://energymanagement.myvaillant.com/files/login/AGB_2025_06.pdf

[^2_15]: https://www.youtube.com/watch?v=GKJ96-WoyG0

[^2_16]: https://www.youtube.com/watch?v=6L-H1z3ozBc

[^2_17]: https://community.home-assistant.io/t/epex-spot-and-awattar-electricity-prices/519151

[^2_18]: https://community.home-assistant.io/t/custom-integration-smartprice-belgium-live-epex-spot-prices-for-automations/1013513

[^2_19]: https://www.youtube.com/watch?v=qIvM8scMlh8

[^2_20]: https://github.com/david-rapan/ha-solcast

[^2_21]: https://community.home-assistant.io/t/epex-spot-and-awattar-electricity-prices/519151?page=6

[^2_22]: https://community-smarthome.com/t/praezise-solarprognose-in-home-assistant-mit-solcast-ganz-einfach/5114

[^2_23]: http://haeo.io/user-guide/installation/

[^2_24]: http://haeo.io/user-guide/removal/

[^2_25]: https://www.hacs.xyz

[^2_26]: https://gathering.tweakers.net/forum/list_messages/2336502

[^2_27]: https://www.youtube.com/watch?v=RozGUafkges\&vl=de

[^2_28]: https://www.hacs.xyz/docs/use/download/download/

[^2_29]: https://github.com/hass-energy/haeo

[^2_30]: https://www.home-assistant.io/blog/2024/08/21/hacs-the-best-way-to-share-community-made-projects/

[^2_31]: https://www.hacs.xyz/docs/use/configuration/basic/

[^2_32]: https://www.cloudapp.dev/de-DE/why-the-home-assistant-community-store-hacs-is-a-must-have-for-every-ha-user

[^2_33]: https://www.youtube.com/watch?v=TL2eqsjhmTE\&vl=de-DE


---

# Wie bekomme ich veränderliche Netzentgelte nach §14a EnWG Modul 3 abgebildet?

Kurz gesagt: Modul 3 liefert dir ein **zeitvariables Netzentgelt mit Hoch‑/Standard‑/Niedertarif**, das der Netzbetreiber veröffentlicht; du bildest es technisch als **zeitabhängigen Netzentgelt‑Teilsensor**, addierst ihn zum Energiepreis (z. B. EPEX‑Spot) und gibst die Summe als „Grid‑Preis“ an HAEO bzw. deine Optimierung weiter.[^3_1][^3_2][^3_3]

***

## Was Modul 3 technisch bedeutet

- Modul 3 ist ein Zusatz zu Modul 1: du bekommst weiterhin die pauschale Netzentgelt‑Reduzierung (Modul 1) und zusätzlich ein zeitvariables Netzentgelt nach Tageszeiten.[^3_4][^3_5][^3_1]
- Der Netzbetreiber definiert mindestens drei Zeitfenster: Hochtarif (HT, mind. 2 h/Tag), Standardtarif (ST) und Niedertarif (NT, typischerweise 10–40 % unter ST).[^3_5][^3_6][^3_1]
- Ab April 2025 müssen Verteilnetzbetreiber diese zeitvariablen Netzentgelte veröffentlichen und anwenden, zunächst für Kunden mit steuerbaren Verbrauchseinrichtungen (Wärmepumpe, Wallbox etc.).[^3_7][^3_6][^3_1]

Wichtig: Das zeitvariable Netzentgelt ist **ein eigener Preisbaustein**, der zum Energiepreis (z. B. Börsenpreis + Lieferantenaufschlag) addiert wird.[^3_3][^3_8]

***

## Datenquelle für die Netzentgelte

Praktisch brauchst du pro Zeitslot (z. B. stündlich) einen Wert in €/kWh, der das Modul‑3‑Netzentgelt enthält:

- Der Netzbetreiber veröffentlicht in der Regel **PDFs oder Tabellen** mit HT/ST/NT‑Zeiten und zugehörigen Arbeitspreisen (oder Aufschlägen) für Modul 3.[^3_9][^3_7][^3_5]
- Dein Lieferant (z. B. Tibber \& Co.) kann das Modul‑3‑Netzentgelt in der eigenen Preis‑API bereits eingerechnet anbieten; einige liefern dafür eigene Zeitreihen‑Sensoren.[^3_8][^3_6]
- Im Worst Case musst du die HT/ST/NT‑Zeiten aus den Netzbetreiber‑Unterlagen einmal manuell in Home Assistant abbilden (z. B. als `input_select`/Schedule + festen Preiswerten) und daraus einen dynamischen Netzentgelt‑Sensor berechnen.[^3_7][^3_5]

Für Görlitz/Sachsen wird voraussichtlich SachsenNetze bzw. der zuständige VNB entsprechende Modul‑3‑Dokumente bereitstellen; dort holst du dir die konkrete Tabelle.[^3_7]

***

## Modellierung in Home Assistant

### 1. Netzentgelt‑Sensor bauen

Du modellierst das Netzentgelt modular:

- Ein Template‑Sensor `sensor.grid_network_fee_modul3` mit Wert in €/kWh.
- Logik:
    - Wenn Uhrzeit in NT‑Fenster → `fee_nt` (z. B. 0,xx €/kWh).
    - Wenn Uhrzeit in HT‑Fenster → `fee_ht`.
    - Sonst → `fee_st`.
- Die konkreten Werte kommen aus den veröffentlichten Modul‑3‑Tabellen deines Netzbetreibers bzw. Lieferanten.[^3_2][^3_6][^3_5]

Optional kannst du über einen `schedule`/`helpers` die täglichen Zeitfenster gepflegt halten und bei Änderungen (neue Quartalswerte) einfach anpassen.[^3_5]

### 2. Gesamtstrompreis‑Sensor

Dann definierst du einen Gesamtpreis‑Sensor:

- `sensor.total_price_hp` = `sensor.epex_effective_price` (Energie + Lieferantenaufschlag + USt) **+** `sensor.grid_network_fee_modul3`.[^3_10][^3_11][^3_3]
- Für andere Verbraucher ohne Modul‑3‑Netzentgelt nutzt du einen eigenen Preis‑Sensor ohne den Modul‑3‑Baustein, damit Haushalte ohne steuVE nicht benachteiligt werden – genau das fordert die BNetzA‑Festlegung.[^3_3][^3_4]

Damit hast du einen **zeitvariablen Gesamtpreis**, der bereits die Netzentgelte nach Modul 3 abbildet.

***

## Einbindung in HAEO / Optimierung

In deiner HAEO‑Konfiguration setzt du die Preisquelle des Grid‑Elements auf den Gesamtpreis‑Sensor:

- Grid‑Element: `price_sensor` = `sensor.total_price_hp`.[^3_12][^3_13]
- HAEO optimiert dann automatisch gegen diesen Gesamtpreis und verschiebt Lasten (Wärmepumpe, EV) in NT‑Fenster bzw. günstige ST‑Zeitfenster; HT‑Zeiten werden gemieden.[^3_13][^3_14][^3_15]

Damit reagiert deine Optimierung nicht nur auf EPEX‑Spot‑Preise, sondern exakt auf das **effektive Arbeitspreisprofil inklusive Modul‑3‑Netzentgelte**, so wie es später abgerechnet wird.[^3_1][^3_8][^3_3]

***

## Abgrenzung: Modul 1 vs. Modul 3 in der Modellierung

- Modul 1 (pauschale Entlastung) kannst du im Modell ignorieren oder als konstante Reduktion im Gesamtpreis berücksichtigen – ändert keine Relativpreise über den Tag.[^3_16][^3_1][^3_5]
- Modul 3 ist für die **zeitliche Steuerung** entscheidend; deswegen bildet man nur diesen Teil als variable Komponente in Sensoren und Optimierung ab.[^3_6][^3_2][^3_1]

Kurz: Du brauchst **kein eigenes „Modul‑3‑Objekt“ in HA**, sondern einen sauber modellierten Preis‑Sensor, der die veröffentlichten HT/ST/NT‑Netzentgelte pro Zeitslot enthält und als Basis für HAEO, Automationen und deine EEBUS‑Steuerlogik dient.[^3_13][^3_3]
<span style="display:none">[^3_17][^3_18][^3_19][^3_20][^3_21]</span>

<div align="center">⁂</div>

[^3_1]: https://kiwigrid.com/de/artikel/14a-enwg-modul-3-wie-stromkundinnen-von-zeitvariablen-netzentgelten-profitieren-koennen

[^3_2]: https://www.bdew.de/media/documents/BDEW-AWH_Modul_3_V1.1_Korrektur070225.pdf

[^3_3]: https://www.bundesnetzagentur.de/DE/Beschlusskammern/BK08/BK8_06_Netzentgelte/68_Para14a_EnWG/Downloads/BK8-22-0010-A_erlaeuternde_Praesentation_zweite_Kons.pdf?__blob=publicationFile\&v=1

[^3_4]: https://www.bundesnetzagentur.de/DE/Beschlusskammern/BK08/BK8_06_Netzentgelte/68_Para14a_EnWG/BK8_14a_EnWG.html

[^3_5]: https://www.bet-consulting.de/newsletter-anmeldung/newsletter-fuer-netzbetreiber-02/2024/artikel/netzentgelte-zeitvariabel-anpassen-mit-dem-instrument-14a-enwg-modul-3

[^3_6]: https://spotmyenergy.de/atlas/dynamische-netzentgelte-§-14a-enwg-modul-3/

[^3_7]: https://www.sachsen-netze.de/wps/portal/netze/cms/menu_main/netzanschluss/niederspannung-strom/steuerung-von-anlagen

[^3_8]: https://support.tibber.com/de/articles/10944270-zeitvariable-netzentgelte-nach-14a

[^3_9]: https://stw-toelz.de/fileadmin/user_upload/20251015_vorl%C3%A4ufige_Netzentgelte_2026_gem._14a_EnWG_Modul_1-3.pdf

[^3_10]: https://github.com/mampfes/ha_epex_spot

[^3_11]: https://community.home-assistant.io/t/epex-spot-and-awattar-electricity-prices/519151?page=3

[^3_12]: https://haeo.io/user-guide/configuration/

[^3_13]: https://haeo.io

[^3_14]: http://haeo.io/developer-guide/architecture/

[^3_15]: https://haeo.io/user-guide/historical-load-forecast/

[^3_16]: https://www.finanztip.de/stromtarife/steuerbare-verbrauchseinrichtungen-14a-enwg/

[^3_17]: https://www.yello.de/wissen/energiemarkt/14a-enwg-netzentgelt/

[^3_18]: https://www.bundesnetzagentur.de/DE/Vportal/Energie/SteuerbareVBE/start.html

[^3_19]: https://www.netze-bw.de/neuregelung-14a-enwg

[^3_20]: https://netze.ewa-altenburg.de/files/1910DA7D751/FAQ Änderung §14a zum 01.01.2024 Kunde.pdf

[^3_21]: https://www.e-netz-suedhessen.de/fileadmin/download/Allgemeine_Bedingungen_14a_EnWG.pdf

