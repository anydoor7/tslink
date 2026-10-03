<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Αποκτήστε πρόσβαση και διαχειριστείτε τις εφαρμογές σας από παντού.<br>Κρατήστε τις ιδιωτικές ή μοιραστείτε τις με τους δικούς σας όρους.</strong></p>

Οι εφαρμογές σας στον υπολογιστή ή στον διακομιστή cloud σας: πρόσβαση μέσω κρυπτογραφημένου ιδιωτικού δικτύου ή, αν το επιλέξετε, με συνδέσμους επισκεπτών στο πρόγραμμα περιήγησης ή δημόσια πρόσβαση. Διαχειριστείτε τις εσείς ή ένας πράκτορας.

<p align="center"><a href="#quickstart">Γρήγορη εκκίνηση</a> · <a href="#agents">Για πράκτορες</a> · <a href="#documentation">Τεκμηρίωση</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <strong>Ελληνικά</strong> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Οι εφαρμογές σας πάντα κοντά

| Ανάγκη | Τι προσφέρει το TSLink |
|---|---|
| Χρήση των εφαρμογών σας σε πολλές συσκευές | Ιδιωτικές διευθύνσεις για οικιακούς πίνακες ελέγχου, τοπικές ιστοσελίδες, αρχεία, API μοντέλων και υπηρεσίες TCP σε υπολογιστή ή διακομιστή. |
| Κοινή χρήση με συγκεκριμένα άτομα | Επιλεγμένες εφαρμογές HTTP/αρχείων, επαληθευμένη ταυτότητα Tailscale, λήξη και ανάκληση. Οι παραλήπτες χρειάζονται Tailscale. [Άτομα](people.md) |
| Επίσκεψη από το πρόγραμμα περιήγησης | Προσωρινοί σύνδεσμοι με προαιρετικό PIN για εφαρμογές μεσολάβησης HTTP ή ρητά δημόσιο HTTPS μέσω Funnel. Οι σύνδεσμοι προωθούνται και δεν πιστοποιούν ταυτότητα. [Επισκέπτες](guest-links.md) |
| Διαχείριση πολλών εφαρμογών | Κατάλογος ανά μηχάνημα, ιδιωτική πύλη, έλεγχοι υγείας και ειδοποιήσεις, ιστορικό πρόσβασης και CLI/MCP με ρόλους πρακτόρων, όρια ανά εφαρμογή και εγγραφές ελέγχου. [Πύλη](portal.md) · [Δικαιώματα MCP](mcp-scopes.md) |

[Συνταγές εφαρμογών](apps.md), [όρια μεταφόρτωσης](sharing.md), [ευέλικτες διάρκειες](durations.md) και [οδηγίες QR και αιτήματα πρόσβασης](requests.md) διευκολύνουν τη συντήρηση. Περιλαμβάνονται στον παρόντα πηγαίο κώδικα.

<a id="installation"></a>
<a id="quickstart"></a>

## Γρήγορη εκκίνηση

Εγκατάσταση από τον κώδικα με **Git και Go 1.26.6+**. Έτοιμες εκδόσεις και Homebrew δεν έχουν δημοσιευτεί ακόμη. Οι εντολές χρησιμοποιούν bash/zsh. [Ρύθμιση macOS, Linux και Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Χρειάζεστε πρόσβαση στο αποθετήριο, **λογαριασμό Tailscale**, [MagicDNS και HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Οι συσκευές ιδιωτικής πρόσβασης χρειάζονται Tailscale και άδεια από την πολιτική δικτύου. Το TSLink ενσωματώνει το Tailscale στο μηχάνημα των εφαρμογών.

Αν η εφαρμογή εκτελείται ήδη στη θύρα 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Επιλέξτε ελεύθερο όνομα· αν το `share` επιστρέψει άλλο, χρησιμοποιήστε το στο `url`. Ολοκληρώστε πρώτα την εγγραφή στο πρόγραμμα περιήγησης και την έγκριση συσκευής που ζητούνται, έπειτα ανοίξτε το ακριβές URL από επιτρεπόμενη συσκευή. Το `share` ξεκινά την υπηρεσία παρασκηνίου όταν χρειάζεται. Η πρώτη ιδιωτική χρήση δεν απαιτεί διακριτικό API διαχειριστή. Μοιραστείτε αρχεία με `tslink share ./report.html`· πρέπει να υπάρχουν και οι εφαρμογές να εκτελούνται. [Πλήρης ρύθμιση](getting-started.md)

Όταν σας φανεί χρήσιμο στην πράξη, μπορείτε να [δώσετε ένα αστέρι στο TSLink](https://github.com/anydoor7/tslink), βοηθώντας άλλους να το βρουν. Είναι απολύτως προαιρετικό.

<a id="architecture"></a>

## Πώς λειτουργεί

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Ένας υπολογιστής ή διακομιστής cloud: CLI/MCP διαχειρίζεται κοινό δαίμονα και κόμβους ανά εφαρμογή. Ιδιωτικές συσκευές συνδέονται με κρυπτογραφημένο Tailscale· προαιρετικό δημόσιο HTTPS/Funnel περνά από έλεγχο επισκέπτη ή ρητή ανοιχτή δημοσίευση προς εφαρμογές HTTP." width="960">
</picture>

Σκεφτείτε μια ιδιωτική, κρυπτογραφημένη διαδρομή προς τις εφαρμογές σας. **Το Tailscale παρέχει μεταφορά δικτύου και HTTPS· το TSLink διαχειρίζεται την πρόσβαση ανά μηχάνημα.** Ένας δαίμονας εκτελεί ξεχωριστό ενσωματωμένο κόμβο ανά υπηρεσία. Η ιδιωτική πύλη δείχνει τις επιτρεπόμενες εφαρμογές· υγεία και ιστορικό βοηθούν στη συντήρηση.

Η δημόσια πρόσβαση ενεργοποιείται ρητά: οι επισκέπτες χρειάζονται σύνδεσμο και προαιρετικό PIN· το ανοιχτό Funnel είναι προσβάσιμο σε όποιον έχει το URL. Και τα δύο χρησιμοποιούν δημόσιο HTTPS, όχι ιδιωτική ταυτότητα χρήστη. Το απλό TCP παραμένει ιδιωτικό, με πολιτική tailnet και πιστοποίηση στο backend. Το TSLink δεν εγκαθιστά εφαρμογές, δεν απομονώνει διεργασίες, δεν δημιουργεί cloud VPC και δεν ενοποιεί πολλά μηχανήματα. Ανεξάρτητο έργο που συνεργάζεται με το Tailscale. [Αρχιτεκτονική και όρια](architecture.md)

<a id="agents"></a>

## Για πράκτορες

Διαχειριστείτε κατάλογο, υγεία, URL και πρόσβαση μέσω CLI/MCP. Ξεκινήστε από τον [οδηγό πρακτόρων](agent-quickstart.md), διαβάστε τα τρέχοντα σχήματα εργαλείων και ελέγξτε την πραγματική πρόσβαση πριν αναφέρετε επιτυχία.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

Η αυτοματοποίηση CLI χρησιμοποιεί `--json`· το MCP χρησιμοποιεί JSON-RPC μέσω stdio. [Πελάτες](mcp-clients.md) · [Απομακρυσμένο MCP](remote-mcp.md) · [Ρόλοι και όρια](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Τεκμηρίωση και άδεια

[Όλοι οι οδηγοί](INDEX.md) · [Αναφορά CLI](cli-reference.md) · [Τοπική AI](local-ai.md) · [Υγεία](health-and-alerts.md) · [Ιστορικό πρόσβασης](access-log.md) · [Σχέδιο ανάπτυξης](roadmap.md)

Ο κατάλογος πολλών μηχανημάτων είναι προγραμματισμένος. Καλωσορίζουμε [συνεισφορές](../CONTRIBUTING.md) και [αναφορές ασφαλείας](../SECURITY.md). Η [Apache 2.0](../LICENSE) επιτρέπει εμπορική χρήση· διατηρήστε [NOTICE](../NOTICE) και [δηλώσεις τρίτων](../THIRD_PARTY_NOTICES.md) στην αναδιανομή. Η [εμπορική συνεργασία](../COMMERCIAL.md) είναι εθελοντική. Οι όροι και τα προγράμματα Tailscale ισχύουν χωριστά.
