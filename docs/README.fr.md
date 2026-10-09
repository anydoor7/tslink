<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Des adresses privées pour vos applications, sur votre réseau Tailscale.</strong></p>
<p align="center">Ouvrez-les depuis vos propres appareils. Partagez-en une avec une personne ou par un lien, jusqu'à la date de votre choix.</p>
<p align="center"><strong>Français</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Autres langues</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installation

Les hôtes macOS nécessitent macOS 13 Ventura ou une version ultérieure ([plateformes prises en charge](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Sous Windows, installez avec Scoop :

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Pour mettre à jour, exécutez `brew upgrade --cask tslink` ou `scoop update; scoop update tslink`. Si TSLink fonctionne comme service en arrière-plan, relancez ensuite `tslink install`.

Les paquets Linux `.deb` et `.rpm` et les versions Windows se trouvent dans la [dernière version](https://github.com/anydoor7/tslink/releases/latest). La première fois que vous partagez une application, TSLink affiche un lien de connexion Tailscale pour celle-ci. [Prise en main](getting-started.md)

<a id="why"></a>

## Quand vous avez besoin de TSLink

Pour une application sur vos propres appareils, Serve suffit. TSLink réunit adresses d'applications, échéances et changements d'accès dans un seul flux.

| Besoin | Tailscale seul | TSLink |
|---|---|---|
| Une application web sur votre téléphone | `tailscale serve 3000` suffit | `tslink share 3000` |
| Plusieurs applications, chacune son nom | Configurer Services, ou des nœuds séparés | Un `share`/`add` par application ; inscrire chaque nœud |
| Une personne, une application, sept jours | Règles de politique, puis un outil JIT ou un retrait manuel | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/fichiers) |
| Lien navigateur, trois jours | Funnel public ; ajouter un contrôle d'accès et un arrêt programmé | `tslink guest create photos --for 3d --public --print-link` (HTTP uniquement) |

Les destinataires privés ont besoin de Tailscale. Les liens invités sont publics, transférables et servent de clés d'accès.

[Comparaison complète](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Vos applications, sur vos propres appareils

- **Une adresse par application.** Applications web, dossiers, fichiers isolés et ports TCP reçoivent chacun leur propre nom dans votre tailnet : vous utilisez des noms plutôt que des adresses IP.
- **Privé par défaut.** Rien n'est public tant que vous ne créez pas de lien invité ou ne publiez pas via Funnel.
- **Une application, pas toute la machine.** Chaque application que vous publiez a son propre nœud, qui ne transmet qu'à cette application. Sans l'application Tailscale sur l'hôte, TSLink n'ajoute aucun autre port de l'hôte à votre tailnet.
- **Une page d'accueil** qui liste vos applications et leur état. [Portail](portal.md)
- **Contrôles de santé et alertes** par commande ou webhook, et un journal d'accès qui inclut les requêtes refusées. [Santé et alertes](health-and-alerts.md) · [Historique d'accès](access-log.md)
- **Des recettes pour 15 applications auto-hébergées**, dont Home Assistant, Jellyfin, Immich et Ollama. `tslink apps detect` trouve celles qui tournent déjà. [Recettes d'applications](apps.md)

## Partager quand vous le voulez

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Les liens invités et les nouvelles URL publiques expirent et ne fonctionnent que pour les applications web ; dossiers, fichiers et ports TCP restent privés. [Personnes](people.md) · [Liens invités](guest-links.md) · [Accès public](funnel.md)

<a id="agents"></a>

## Pour les agents IA

Un serveur de développement qu'un agent lance sur `localhost` est hors de portée de votre téléphone. TSLink permet à l'agent de lui donner une adresse privée, d'indiquer l'URL exacte et de la retirer une fois terminé.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI ou MCP.** Les commandes de gestion acceptent `--json` et renvoient des résultats versionnés ; `tslink mcp` propose les opérations sur les applications et les accès via MCP.
- **Un fichier, pas le dossier.** Un agent peut partager uniquement son rapport HTML avec `tslink share ./report.html` ; les autres fichiers de ce dossier restent inaccessibles.
- **Rôles limités.** `viewer`, `app-operator` ou `people-manager`, restreints aux applications que vous désignez. `tslink mcp-audit` montre ce qu'un agent a modifié. Les rôles limitent les outils de TSLink, pas le shell de l'agent lui-même.

[Guide rapide des agents](agent-quickstart.md) · [Droits MCP](mcp-scopes.md) · [MCP distant](remote-mcp.md)

<a id="architecture"></a>

## Fonctionnement

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC ou serveur cloud : CLI/MCP gère un démon commun et un nœud par application. Les appareils privés passent par Tailscale chiffré ; HTTPS/Funnel public, facultatif, accède aux applications HTTP via un contrôle invité ou une publication explicitement ouverte." width="960">
</picture>

Un processus en arrière-plan fait tourner un nœud Tailscale distinct pour chaque application. Tailscale fournit le transport du tailnet et les certificats HTTPS. L'accès privé au web et aux fichiers peut être limité par identité Tailscale avec `--allow` et les accès par personne ; le TCP brut repose sur la politique de votre tailnet et sur la connexion propre à l'application. [Architecture](architecture.md)

<a id="requirements"></a>

## Prérequis

| Qui | Besoin |
|---|---|
| Vous | Un compte Tailscale avec MagicDNS et HTTPS activés |
| La machine qui fait tourner vos applications | TSLink, qui intègre Tailscale (sous Linux, une session utilisateur systemd) |
| Vos appareils et les personnes avec qui vous partagez | L'application Tailscale |
| Invités | Un navigateur |

Les noms des applications HTTPS apparaissent dans des journaux publics de certificats : choisissez des noms que d'autres peuvent voir sans gêne.

<a id="documentation"></a>

## Plus

[Toute la documentation](INDEX.md) · [Référence CLI](cli-reference.md) · [Comparaison avec Serve, ngrok et Cloudflare](comparison.md) · [Contribuer](../CONTRIBUTING.md) · [Sécurité](../SECURITY.md)

Apache 2.0. TSLink est un projet indépendant, ni conçu ni approuvé par Tailscale.
