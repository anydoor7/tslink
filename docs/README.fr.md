<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Donnez à chaque application de votre ordinateur ou serveur sa propre adresse privée sur votre réseau Tailscale, et décidez qui peut y accéder.</strong></p>

Ouvrez vos applications web, dossiers, API de modèles et bases de données depuis votre propre téléphone et votre ordinateur portable, avec contrôle de santé et historique d'accès pour chacun. Vos agents IA peuvent donner aux applications qu'ils lancent sur localhost une adresse privée pour vos autres appareils, et les vérifier, dans les limites du rôle que vous leur donnez. Quand quelqu'un d'autre a besoin d'y accéder, accordez l'accès à une personne nommée jusqu'à une date, ou ouvrez une application web sur Internet pour une durée limitée.

**Tailscale requis.** Il vous faut un compte Tailscale (gratuit pour un usage personnel), et chaque appareil qui ouvre une application privée a besoin de l'application Tailscale ; les invités et visiteurs publics n'ont besoin que d'un navigateur. TSLink est un projet indépendant, ni conçu ni approuvé par Tailscale. [Prérequis](#requirements)

<p align="center"><a href="#quickstart">Démarrage rapide</a> · <a href="#agents">Pour les agents</a> · <a href="comparison.md">Comparaison avec Serve, ngrok et Cloudflare</a> · <a href="#documentation">Documentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <strong>Français</strong> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Ce que vous pouvez faire

### Accéder à vos propres applications

- **Une adresse par application.** `tslink share 3000`, `tslink share ./photos` ou `tslink add db --tcp localhost:5432` donne à une application web, un dossier, un fichier ou un service TCP sa propre adresse privée dans votre tailnet (votre réseau Tailscale privé), par exemple `https://photos.<tailnet>.ts.net`. Chaque application est un appareil Tailscale distinct : vous ouvrez donc les applications par leur nom plutôt que par adresse IP et port.
- **Privé sauf choix contraire.** TSLink garde l'accès aux applications privé par défaut, et les règles de votre tailnet décident quels appareils peuvent se connecter. Il n'ouvre un point d'accès public que si vous créez un lien invité ou publiez explicitement via Funnel.
- **Tout au même endroit.** `tslink status --urls` liste les applications enregistrées sur cet ordinateur, et une page d'accueil privée facultative affiche leurs adresses et leur état. [Portail](portal.md)
- **Savoir quand quelque chose casse.** Des contrôles de santé en arrière-plan peuvent vous prévenir par une commande ou un webhook quand une application tombe ou revient, ou quand sa connexion Tailscale va bientôt expirer. L'historique d'accès indique qui a ouvert quelle application et quand, y compris les requêtes refusées. [Santé et alertes](health-and-alerts.md) · [Historique d'accès](access-log.md)
- **Recettes d'applications.** Des recettes couvrent 15 applications auto-hébergées, dont Home Assistant, Jellyfin, Immich et Ollama, et `tslink apps detect` peut repérer les applications prises en charge qui écoutent déjà en local. Pour les gros envois de photos et de vidéos, [relevez les limites d'envoi de l'application concernée](sharing.md). [Recettes d'applications](apps.md) · [IA locale](local-ai.md)

### Laisser vos agents s'en charger

Quand un agent lance un serveur de développement, un aperçu ou une API de modèle locale sur `localhost`, votre téléphone et vos autres ordinateurs ne peuvent pas joindre cette adresse. TSLink permet à l'agent de lui donner une adresse privée, de vous indiquer l'URL exacte et de retirer ensuite son enregistrement, dans les limites que vous fixez.

- **Partager, vérifier, annuler.** `share --json` renvoie le nom enregistré, ainsi que l'URL exacte ou un lien de connexion à ouvrir. `url <name> --wait` et `status --urls --name <name>` indiquent si le point d'accès est prêt, et `remove <name>` (`unshare` dans MCP) retire le partage. [Guide rapide des agents](agent-quickstart.md)
- **Conçu pour l'automatisation.** Les commandes de gestion autres que `tslink mcp` acceptent `--json` et renvoient des résultats versionnés avec des codes d'erreur stables. `tslink mcp` expose les outils d'applications et d'accès à un client MCP local via JSON-RPC. Une fois les liaisons d'appelants configurées, `tslink serve --mcp` expose ces outils aux clients MCP de vos autres appareils via le tailnet. [Automatisation JSON](json-automation.md) · [MCP distant](remote-mcp.md)
- **Autorité limitée.** Un agent local a l'autorité de propriétaire par défaut. Donnez à un agent un rôle réduit (`viewer`, `app-operator` ou `people-manager`) qui ne couvre que les applications que vous nommez et plafonne la durée des accès qu'il accorde. Les modifications faites via MCP sont enregistrées, et `tslink mcp-audit` les affiche. Les rôles limitent les outils de TSLink, pas le shell ni les fichiers de l'agent lui-même. [Droits MCP](mcp-scopes.md)

### Partager avec les personnes de votre choix

- **Des personnes nommées, jusqu'à une date.** `tslink people add alice@example.com --apps photos,notes --for 7d` permet à ce compte Tailscale d'ouvrir ces applications web et de fichiers jusqu'à l'échéance. `people update`, `extend` et `people remove` modifient ou mettent fin à l'accès ; après un retrait, la requête suivante de la personne est refusée, mais ce qu'elle a déjà téléchargé ne peut pas être récupéré. [Personnes](people.md) · [Durées](durations.md)
- **Quelqu'un hors de votre tailnet.** Ajoutez `--invite --print-links` pour obtenir un message prêt à envoyer, avec une invitation d'appareil pour chaque application (il faut un jeton API appartenant à un utilisateur). `--qr` affiche un code pour configurer un téléphone.
- **Demandes.** Les personnes de votre tailnet peuvent demander plus de temps, ou l'accès à une application que vous marquez comme demandable, depuis la page d'accueil. Vous approuvez avec une durée, en une commande. [Demandes d'accès](requests.md)

### Ouvrir une application web sur Internet, pour un temps

- **Liens invités.** `tslink guest create photos --for 3d --public --print-link` crée un lien navigateur vers une application web, avec PIN facultatif, que vous pouvez révoquer à part. Les invités n'ont pas besoin de compte Tailscale. Toute personne qui détient le lien peut l'utiliser, il ne prouve donc pas qui a visité. [Liens invités](guest-links.md)
- **Une URL publique ouverte.** `tslink add preview --proxy localhost:3000 --funnel --public` publie une application web pour toute personne qui a son URL. Par défaut, une nouvelle publication dure 24 heures, et `--funnel-ttl` permet de choisir une autre durée. [Funnel](funnel.md)
- Les nouveaux liens invités et les nouvelles publications publiques ouvertes passent par Tailscale Funnel et ont une durée limitée (au moins 1 heure, au plus 7 jours par défaut, plafond réglable par le propriétaire). Ces accès publics prennent en charge les applications en proxy HTTP ; les services directs de dossiers ou de fichiers et le TCP brut restent privés.

Tout ce qui précède est livré avec la v0.1.0.

<a id="requirements"></a>

## Prérequis

TSLink repose sur Tailscale. C'est un projet indépendant, ni conçu ni approuvé par Tailscale, et les conditions et [offres](https://tailscale.com/pricing) propres à Tailscale s'appliquent.

| Qui | Ce qu'il faut |
|---|---|
| Vous | Un compte Tailscale avec [MagicDNS et HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) activés. L'offre Personal gratuite est réservée à un usage non commercial. |
| L'ordinateur ou le serveur qui fait tourner vos applications | TSLink seulement. Il intègre Tailscale, donc pas d'installation séparée de Tailscale. Avec la configuration par défaut, chaque nouveau nœud d'application demande une connexion dans le navigateur et parfois l'approbation de l'appareil. Des [identifiants enregistrés](credentials-and-tags.md) permettent l'inscription sans connexion dans le navigateur pour chaque application. |
| Vos autres appareils | L'application Tailscale, connectée à votre tailnet. |
| Les personnes de votre choix | L'application Tailscale et leur propre compte. Soit elles rejoignent votre tailnet, ce qui ajoute un utilisateur à votre offre, soit elles acceptent une invitation d'appareil pour chaque application. Les règles de votre tailnet doivent leur en permettre l'accès. |
| Invités et visiteurs publics | Un navigateur. Votre tailnet doit autoriser Funnel, que Tailscale présente encore comme bêta. |

Quand un certificat HTTPS est émis pour une application, son nom d'appareil Tailscale et le nom DNS de votre tailnet apparaissent dans un journal public de certificats. Choisissez des noms d'application que vous acceptez de voir rendus publics.

<a id="installation"></a>
<a id="quickstart"></a>

## Démarrage rapide

Sur macOS ou Linux, installez avec Homebrew. Le binaire macOS est signé avec un certificat Developer ID et notarié par Apple. Pour mettre à jour plus tard, lancez `brew upgrade --cask tslink`, puis de nouveau `tslink install` si TSLink tourne comme service d'arrière-plan.

```bash
brew install --cask anydoor7/tap/tslink
```

Sous Windows, téléchargez `tslink_<version>_windows_<arch>.zip` depuis la [dernière version](https://github.com/anydoor7/tslink/releases/latest), vérifiez-le avec `checksums.txt`, puis lancez `tslink install` pour que TSLink démarre à l'ouverture de session. Le zip n'est pas signé Authenticode ; [vérifiez la version](verify-release.md) grâce à ses sommes de contrôle signées et à ses attestations. Les paquets Linux `.deb` et `.rpm` se trouvent sur la même page de version. Pour compiler depuis les sources, il vous faut **Git et Go 1.26.6+**. Les commandes ci-dessous utilisent bash/zsh ; voir [Configuration macOS, Linux et Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Il faut un **compte Tailscale** et [MagicDNS et HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Les appareils privés destinataires ont besoin de Tailscale et d'une autorisation réseau. TSLink embarque Tailscale sur l'hôte des applications.

Si votre application tourne déjà sur le port 3000 :

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Choisissez un nom libre ; si `share` en renvoie un autre, utilisez-le avec `url`. Terminez d'abord l'inscription dans le navigateur et l'approbation de l'appareil demandées, puis ouvrez l'URL exacte depuis un appareil autorisé. `share` démarre le service en arrière-plan si nécessaire. Ce premier accès privé ne nécessite aucun jeton API administrateur. Partagez un fichier existant avec `tslink share ./report.html` ; les applications doivent déjà fonctionner. [Configuration complète](getting-started.md)

Une fois que tout fonctionne, vous pouvez [ajouter une étoile à TSLink](https://github.com/anydoor7/tslink) pour le faire découvrir. C'est entièrement facultatif.

<a id="architecture"></a>

## Comment tout s'articule

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC ou serveur cloud : CLI/MCP gère un démon commun et un nœud par application. Les appareils privés passent par Tailscale chiffré ; HTTPS/Funnel public, facultatif, accède aux applications HTTP via un contrôle invité ou une publication explicitement ouverte." width="960">
</picture>

Imaginez un chemin privé et chiffré vers vos applications. **Tailscale fournit le transport réseau et HTTPS ; TSLink gère les accès sur chaque hôte.** Un démon commun exécute un nœud embarqué par service. Le portail privé affiche les applications autorisées ; leur état et l'historique facilitent la maintenance.

L'accès public est volontaire : les invités ont besoin du lien et du PIN éventuel ; Funnel ouvert est accessible à quiconque possède l'URL. Tous deux utilisent HTTPS public, pas l'identité privée d'un utilisateur. Le TCP brut reste privé et dépend des règles du tailnet et de l'authentification du service cible. TSLink n'installe pas les applications, n'isole pas les processus, ne crée pas de VPC cloud et n'agrège pas plusieurs hôtes. Projet indépendant fonctionnant avec Tailscale. [Architecture et limites](architecture.md)

<a id="agents"></a>

## Pour les agents

Gérez inventaire, santé, URL et accès via CLI/MCP. Consultez le [guide rapide des agents](agent-quickstart.md), lisez les schémas actuels des outils et vérifiez l'accès réel avant d'annoncer un succès.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

La CLI utilise `--json` pour l'automatisation ; MCP utilise JSON-RPC sur stdio. [Clients](mcp-clients.md) · [MCP distant](remote-mcp.md) · [Rôles et périmètres](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Documentation et licence

[Tous les guides](INDEX.md) · [Référence CLI](cli-reference.md) · [IA locale](local-ai.md) · [Santé](health-and-alerts.md) · [Historique d'accès](access-log.md) · [Feuille de route](roadmap.md)

L'inventaire multi-hôte est prévu. [Contributions](../CONTRIBUTING.md) et [signalements de sécurité](../SECURITY.md) sont bienvenus. [Apache 2.0](../LICENSE) autorise l'usage commercial ; conservez [NOTICE](../NOTICE) et les [mentions tierces](../THIRD_PARTY_NOTICES.md) lors d'une redistribution. La [coopération commerciale](../COMMERCIAL.md) est volontaire. Les conditions et offres Tailscale s'appliquent séparément.
