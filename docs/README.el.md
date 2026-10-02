<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="λογότυπο TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Δώστε στις τοπικές εφαρμογές, τα μοντέλα και τα αρχεία σας τη δική τους ιδιωτική διεύθυνση.</strong><br>
  Ανοίξτε τα από άλλη συσκευή με άδεια πρόσβασης στο δίκτυό σας Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Άδεια: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 ή νεότερο"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: ενσωματωμένοι κόμβοι tsnet"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 εργαλεία"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <strong>Ελληνικά</strong> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Εγκατάσταση

Χρειάζεστε **Go 1.26.6+** και Git. Δεν έχουν δημοσιευτεί έτοιμες εκδόσεις ή
Homebrew cask· εγκαταστήστε από τον πηγαίο κώδικα. Αυτά τα παραδείγματα χρησιμοποιούν
**bash ή zsh**· δείτε την [υποστήριξη πλατφορμών](platforms.md) για τις απαιτήσεις των Windows και των υπηρεσιών παρασκηνίου.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Χρησιμοποιήστε λογαριασμό Tailscale με [ενεργοποιημένα MagicDNS και HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
Η συσκευή που αποκτά πρόσβαση πρέπει να είναι συνδεδεμένη στο δίκτυό σας Tailscale (**tailnet**), με πολιτική
που επιτρέπει την πρόσβαση στην υπηρεσία. Το TSLink ενσωματώνει το Tailscale στον υπολογιστή που δημοσιεύει την υπηρεσία.

### Μοιραστείτε την πρώτη σας σελίδα

Δημιουργήστε μια σελίδα· το TSLink την εξυπηρετεί απευθείας και εκκινεί την υπηρεσία παρασκηνίου όταν χρειάζεται:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Αν το TSLink εμφανίσει URL εγγραφής, ανοίξτε το για να εξουσιοδοτήσετε τον κόμβο· το tailnet σας μπορεί επίσης να
απαιτεί έγκριση της συσκευής από διαχειριστή. Στη συνέχεια, λάβετε την ακριβή διεύθυνση:

```bash
tslink url demo --wait
```

Ανοίξτε αυτό το URL σε συσκευή με άδεια πρόσβασης. Δεν χρειάζεται διακριτικό API για αυτή την πρώτη κοινοποίηση.
[Πλήρης ρύθμιση και λεπτομέρειες κύκλου ζωής →](getting-started.md)

<a id="use-cases"></a>

## Τι θα μοιραστείτε;

Τα αρχεία πρέπει να υπάρχουν· τα συστήματα υποστήριξης εφαρμογών, βάσεων δεδομένων και μοντέλων πρέπει να εκτελούνται ήδη στις καθορισμένες θύρες.

| Περίπτωση χρήσης | Εντολή |
|---|---|
| Ανοίξτε μια τοπική εφαρμογή από άλλη συσκευή | `tslink share 3000` |
| Περιηγηθείτε σε έναν κατάλογο αρχείων | `tslink share ./public --name files` |
| Διαβάστε μια παραγόμενη αναφορά HTML στο τηλέφωνό σας | `tslink share ./report.html --name report` |
| Συνδεθείτε σε μια τοπική βάση δεδομένων μέσω TCP | `tslink add database --tcp localhost:5432` |
| Χρησιμοποιήστε το HTTP API ενός τοπικού μοντέλου, όπως το Ollama | `tslink add model --proxy localhost:11434` |

Για το Ollama, λάβετε το ακριβές URL με `tslink url model --wait`· η τιμή `baseURL` ενός
πελάτη συμβατού με το OpenAI είναι αυτό το URL μαζί με `/v1`. [Τοπικά μοντέλα και ροές εργασίας με ιδιωτικά δεδομένα →](local-ai.md)

Για πολλές εφαρμογές σε έναν host, το TSLink συνδυάζει ονομασμένους κόμβους, λίστες επιτρεπόμενων ταυτοτήτων HTTP, λήξη Funnel και διαχείριση MCP. Το [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) μπορεί να αρκεί για μία εφαρμογή στις δικές σας συσκευές.

<a id="architecture"></a>

## Αρχιτεκτονική

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Παράδειγμα χάρτη υπηρεσιών: τα App, Docs, Database και Model είναι ξεχωριστοί ονομασμένοι κόμβοι σε ένα tailnet. Οι εφαρμογές, τα αρχεία και τα API μοντέλων χρησιμοποιούν HTTPS· η βάση δεδομένων χρησιμοποιεί ιδιωτικό TCP." width="960">
</picture>

**Ένα tailnet, ξεχωριστοί κόμβοι υπηρεσιών.** Μια κοινή διεργασία παρασκηνίου εκτελεί έναν ενσωματωμένο κόμβο tsnet ανά
υπηρεσία, προωθώντας HTTP, εξυπηρετώντας αρχεία ή διαμεσολαβώντας για TCP. Οι αλλαγές στο μητρώο τίθενται σε ισχύ όσο
εκτελείται. Κάθε κόμβος έχει τη δική του ταυτότητα δικτύου· οι υπηρεσίες μοιράζονται τον υπολογιστή που τις δημοσιεύει.
[Λεπτομέρειες αρχιτεκτονικής →](architecture.md)

| Συστατικό | Ρόλος |
|---|---|
| [Go](../go.mod) | Εγγενές εκτελέσιμο γραμμής εντολών |
| [Tailscale tsnet](architecture.md) | Κόμβοι υπηρεσιών και μεταφορά μέσω tailnet |
| [Cobra](https://github.com/spf13/cobra) | Εντολές και βοήθεια |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Μηχανισμοί μεταφοράς για πράκτορες |
| Κλειδοθήκη του λειτουργικού συστήματος και διαχειριστής υπηρεσιών χρήστη | Προαιρετικά διαπιστευτήρια και λειτουργία στο παρασκήνιο |

Οι υπηρεσίες παραμένουν μέσα στο tailnet σας, εκτός αν ενεργοποιήσετε ρητά το [δημόσιο Funnel](getting-started.md#more-examples).
Οι υπηρεσίες HTTP και αρχείων υποστηρίζουν λίστες επιτρεπόμενων ταυτοτήτων (`WhoIs`, `--allow`)· το TCP χρησιμοποιεί την πολιτική του tailnet και
τον έλεγχο ταυτότητας του ίδιου του συστήματος υποστήριξης. Δείτε τα [όρια κοινοποίησης](sharing.md).

Το TSLink δεν εγκαθιστά εφαρμογές, δεν εκτελεί μοντέλα, δεν απομονώνει διεργασίες του host και δεν συγκεντρώνει πολλούς hosts. Δίκτυο, κρυπτογράφηση και HTTPS παρέχονται από το Tailscale· το TSLink είναι ανεξάρτητο έργο.

<a id="agents"></a>

## Για πράκτορες

Τα **19 εργαλεία MCP** επιτρέπουν σε έναν πράκτορα να μοιράζεται αναφορές, να διαχειρίζεται υπηρεσίες, να ανακτά URL και να ελέγχει
τη ρύθμιση. Συνδέστε έναν τοπικό πελάτη MCP στο εγκατεστημένο εκτελέσιμο:

```json
{
  "mcpServers": {
    "tslink": {
      "command": "tslink",
      "args": ["mcp"]
    }
  }
}
```

Το MCP διαχειρίζεται το TSLink· οι εφαρμογές χρησιμοποιούν το HTTP API του μοντέλου για συμπερασμό.
Δείτε τους [πελάτες MCP](mcp-clients.md), το [απομακρυσμένο MCP](remote-mcp.md) και τον
[οδηγό λειτουργίας για πράκτορες](../AGENTS.md) για ρύθμιση και αυτοματοποίηση.

Ο αυτοματισμός CLI υποστηρίζει `--json` με `schema_version` ίσο με `1`· δείτε `tslink status --urls --json`. Το τοπικό MCP χρησιμοποιεί JSON-RPC μέσω stdio. Δείτε [αυτοματισμό JSON](json-automation.md).

<a id="roadmap"></a>

## Τι έρχεται

Τα στοιχεία Σε συγχώνευση, Σε έλεγχο ή Σχεδιασμένο δεν περιλαμβάνονται στην παραπάνω εγκατάσταση από πηγαίο κώδικα.

| Χρήση | Κατάσταση |
|---|---|
| <!-- roadmap:people --> Δώστε σε συγγενή πρόσβαση σε ιδιωτικές εφαρμογές HTTP/αρχείων για 3 ημέρες, με τις προσκλήσεις σε ένα μήνυμα· ο παραλήπτης εξακολουθεί να χρειάζεται Tailscale. | Σε συγχώνευση |
| <!-- roadmap:health --> Ελέγξτε την υγεία εφαρμογών και λάβετε ειδοποιήσεις διακοπής ή λήξης μέσω προαιρετικής εντολής ή webhook. | Σε συγχώνευση |
| <!-- roadmap:recipes --> Βρείτε υποστηριζόμενες εφαρμογές loopback και προεπισκοπήστε συνταγές αυτοφιλοξενούμενων εφαρμογών πριν την κοινοποίηση. | Σε συγχώνευση |
| <!-- roadmap:limits --> Ορίστε μέγεθος μεταφόρτωσης και χρονικά όρια αιτημάτων ανά εφαρμογή HTTP για μεγάλα αρχεία και αργούς πελάτες. | Σε συγχώνευση |
| <!-- roadmap:windows --> Επανεκκινήστε daemon Windows μετά από κατάρρευση όσο είστε συνδεδεμένοι, με προγραμματισμένη εργασία και ενσωματωμένο επόπτη. | Σε συγχώνευση |
| <!-- roadmap:access-log --> Δείτε ποιος άνοιξε ποια εφαρμογή στα τοπικά αρχεία πρόσβασης, με καταγραφή διαδρομής `prefix`, `full` ή `off`. | Σε έλεγχο |
| <!-- roadmap:portal --> Ανοίξτε μία αρχική σελίδα με επιτρεπόμενες εφαρμογές και παράδοση εγγραφής στους ιδιοκτήτες· οι επισκέπτες εξακολουθούν να χρειάζονται Tailscale. | Σε έλεγχο |
| <!-- roadmap:mcp-scopes --> Δώστε σε έναν agent ρόλο και πεδίο εφαρμογών, με αποδείξεις ελέγχου για τις αλλαγές του. | Σε έλεγχο |
| <!-- roadmap:guest-links --> Αφήστε επισκέπτη να ανοίξει μία εφαρμογή HTTP στον browser χωρίς εγκατάσταση Tailscale, με σύνδεσμο που λήγει και προαιρετικό PIN μέσω δημόσιου Funnel με έλεγχο πρόσβασης. | Σε έλεγχο |
| <!-- roadmap:durations --> Επιλέξτε προεπιλεγμένες ή δικές σας διάρκειες τουλάχιστον 1 ώρας, με ρυθμιζόμενο μέγιστο επισκεπτών 7 ημερών από προεπιλογή. | Σε έλεγχο |
| <!-- roadmap:requests --> Βοηθήστε χρήστες τηλεφώνου να συνδεθούν με QR· επιτρέψτε στον ιδιοκτήτη να εγκρίνει αιτήματα πρόσβασης σε εφαρμογές ή επιπλέον χρόνου σε μία ενέργεια. | Σε έλεγχο |
| <!-- roadmap:multi-host --> Δείτε εφαρμογές πολλών hosts σε έναν κατάλογο. | Σχεδιασμένο |

<a id="documentation"></a>

## Τεκμηρίωση και άδεια

[Ξεκινώντας](getting-started.md) · [Τοπικά μοντέλα](local-ai.md) ·
[Αναφορά CLI](cli-reference.md) · [Πλατφόρμες](platforms.md) · [Οδικός χάρτης](roadmap.md)

Συνεισφέρετε μέσω του [CONTRIBUTING.md](../CONTRIBUTING.md)· αναφέρετε ευπάθειες ακολουθώντας το
[SECURITY.md](../SECURITY.md).

Το TSLink χρησιμοποιεί την ατροποποίητη [άδεια Apache 2.0](../LICENSE), συμπεριλαμβανομένης της εμπορικής χρήσης.
Κατά την αναδιανομή, διατηρήστε το ισχύον [NOTICE](../NOTICE) και τις [ειδοποιήσεις τρίτων](../THIRD_PARTY_NOTICES.md).
Η [εμπορική συνεργασία](../COMMERCIAL.md) είναι προαιρετική και δεν προσθέτει όρους στην άδεια.
Οι όροι υπηρεσίας και τα προγράμματα του Tailscale ισχύουν ξεχωριστά.
