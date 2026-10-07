<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Ιδιωτικές διευθύνσεις για τις εφαρμογές σας, στο δίκτυο Tailscale σας.</strong></p>
<p align="center">Ανοίξτε τες από τις δικές σας συσκευές. Μοιραστείτε μία με ένα άτομο ή με έναν σύνδεσμο, ως την ημερομηνία που διαλέγετε.</p>
<p align="center"><strong>Ελληνικά</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Περισσότερες γλώσσες</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Εγκατάσταση

```sh
brew install --cask anydoor7/tap/tslink
```

Τα πακέτα `.deb` και `.rpm` για Linux και οι εκδόσεις για Windows βρίσκονται στην [τελευταία έκδοση](https://github.com/anydoor7/tslink/releases/latest). Την πρώτη φορά που μοιράζεστε μια εφαρμογή, το TSLink εμφανίζει έναν σύνδεσμο εισόδου στο Tailscale για αυτήν. [Πρώτα βήματα](getting-started.md)

<a id="why"></a>

## Πότε χρειάζεστε το TSLink

Για μία εφαρμογή στις δικές σας συσκευές αρκεί το Serve. Το TSLink συγκεντρώνει διευθύνσεις εφαρμογών, προθεσμίες και αλλαγές πρόσβασης σε μία ροή εργασίας.

| Εργασία | Μόνο Tailscale | TSLink |
|---|---|---|
| Μία εφαρμογή web στο κινητό | Αρκεί το `tailscale serve 3000` | `tslink share 3000` |
| Πολλές εφαρμογές, καθεμία με δικό της όνομα | Ρύθμιση Services ή ξεχωριστοί κόμβοι | Ένα `share`/`add` ανά εφαρμογή, με εγγραφή κάθε κόμβου |
| Ένα άτομο, μία εφαρμογή, επτά ημέρες | Κανόνες πολιτικής, μετά εργαλείο JIT ή χειροκίνητη αφαίρεση | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/αρχεία) |
| Σύνδεσμος για περιηγητή, τρεις ημέρες | Δημόσιο Funnel, με δικό σας έλεγχο πρόσβασης και προγραμματισμένο τερματισμό | `tslink guest create photos --for 3d --public --print-link` (μόνο HTTP) |

Οι ιδιωτικοί παραλήπτες χρειάζονται Tailscale. Οι σύνδεσμοι επισκεπτών είναι δημόσιοι: όποιος έχει τον σύνδεσμο μπορεί να τον ανοίξει και να τον προωθήσει.

[Πλήρης σύγκριση](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Οι εφαρμογές σας, στις δικές σας συσκευές

- **Μια διεύθυνση για κάθε εφαρμογή.** Εφαρμογές web, φάκελοι, μεμονωμένα αρχεία και θύρες TCP παίρνουν το καθένα δικό του όνομα στο tailnet σας, οπότε χρησιμοποιείτε ονόματα αντί για διευθύνσεις IP.
- **Ιδιωτικό από προεπιλογή.** Τίποτα δεν γίνεται δημόσιο μέχρι να δημιουργήσετε σύνδεσμο επισκέπτη ή να δημοσιεύσετε μέσω Funnel.
- **Μια αρχική σελίδα** που δείχνει τις εφαρμογές σας με την κατάστασή τους. [Πύλη](portal.md)
- **Έλεγχοι υγείας και ειδοποιήσεις** μέσω εντολής ή webhook, και ένα αρχείο πρόσβασης που περιλαμβάνει τα αιτήματα που απορρίφθηκαν. [Υγεία και ειδοποιήσεις](health-and-alerts.md) · [Ιστορικό πρόσβασης](access-log.md)
- **Συνταγές για 15 αυτοφιλοξενούμενες εφαρμογές**, ανάμεσά τους Home Assistant, Jellyfin, Immich και Ollama. Το `tslink apps detect` βρίσκει όσες τρέχουν ήδη. [Συνταγές εφαρμογών](apps.md)

## Μοιραστείτε όταν θέλετε

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Οι σύνδεσμοι επισκεπτών και τα νέα δημόσια URL λήγουν και λειτουργούν μόνο για εφαρμογές web. Φάκελοι, αρχεία και θύρες TCP μένουν ιδιωτικά. [Άτομα](people.md) · [Σύνδεσμοι επισκεπτών](guest-links.md) · [Δημόσια πρόσβαση](funnel.md)

<a id="agents"></a>

## Για πράκτορες AI

Έναν διακομιστή ανάπτυξης που ξεκινά ένας πράκτορας στο `localhost` δεν τον φτάνετε από το κινητό σας. Το TSLink επιτρέπει στον πράκτορα να του δώσει ιδιωτική διεύθυνση, να αναφέρει το ακριβές URL και να την αφαιρέσει όταν τελειώσει.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI ή MCP.** Οι εντολές διαχείρισης δέχονται `--json` και επιστρέφουν αποτελέσματα με έκδοση. Το `tslink mcp` προσφέρει λειτουργίες εφαρμογών και πρόσβασης μέσω MCP.
- **Περιορισμένοι ρόλοι.** `viewer`, `app-operator` ή `people-manager`, μόνο για τις εφαρμογές που ορίζετε. Το `tslink mcp-audit` δείχνει τι άλλαξε ένας πράκτορας. Οι ρόλοι περιορίζουν τα εργαλεία του TSLink, όχι το ίδιο το shell του πράκτορα.

[Οδηγός πρακτόρων](agent-quickstart.md) · [Δικαιώματα MCP](mcp-scopes.md) · [Απομακρυσμένο MCP](remote-mcp.md)

<a id="architecture"></a>

## Πώς λειτουργεί

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Ένας υπολογιστής ή διακομιστής cloud: CLI/MCP διαχειρίζεται κοινό δαίμονα και κόμβους ανά εφαρμογή. Ιδιωτικές συσκευές συνδέονται με κρυπτογραφημένο Tailscale· προαιρετικό δημόσιο HTTPS/Funnel περνά από έλεγχο επισκέπτη ή ρητή ανοιχτή δημοσίευση προς εφαρμογές HTTP." width="960">
</picture>

Μία διεργασία παρασκηνίου τρέχει ξεχωριστό κόμβο Tailscale για κάθε εφαρμογή. Το Tailscale παρέχει τη μεταφορά μέσα στο tailnet και τα πιστοποιητικά HTTPS. Η ιδιωτική πρόσβαση σε web και αρχεία μπορεί να περιοριστεί ανά ταυτότητα Tailscale με `--allow` και άδειες ανά άτομο. Το σκέτο TCP βασίζεται στην πολιτική του tailnet σας και στη σύνδεση της ίδιας της εφαρμογής. [Αρχιτεκτονική](architecture.md)

<a id="requirements"></a>

## Απαιτήσεις

| Ποιος | Χρειάζεται |
|---|---|
| Εσείς | Λογαριασμό Tailscale με ενεργά MagicDNS και HTTPS |
| Το μηχάνημα που τρέχει τις εφαρμογές σας | Το TSLink, που ενσωματώνει το Tailscale (στο Linux, μια συνεδρία χρήστη systemd) |
| Οι συσκευές σας και τα άτομα με τα οποία μοιράζεστε εφαρμογές | Την εφαρμογή Tailscale |
| Επισκέπτες | Έναν περιηγητή |

Τα ονόματα των εφαρμογών HTTPS εμφανίζονται σε δημόσια αρχεία καταγραφής πιστοποιητικών, οπότε διαλέξτε ονόματα που δεν σας πειράζει να δουν άλλοι.

<a id="documentation"></a>

## Περισσότερα

[Όλη η τεκμηρίωση](INDEX.md) · [Αναφορά CLI](cli-reference.md) · [Σύγκριση με Serve, ngrok και Cloudflare](comparison.md) · [Συνεισφορά](../CONTRIBUTING.md) · [Ασφάλεια](../SECURITY.md)

Apache 2.0. Το TSLink είναι ανεξάρτητο έργο· δεν το έφτιαξε ούτε το ενέκρινε η Tailscale.
