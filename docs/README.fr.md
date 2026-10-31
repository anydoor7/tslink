<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink est une CLI et un serveur MCP open source qui donne à chaque application de votre ordinateur une adresse privée <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, pour que vous ou votre coding agent puissiez ouvrir sur votre téléphone un serveur de développement en cours d'exécution. Projet indépendant, non édité par Tailscale.</p>
<p align="center">Votre téléphone a besoin de l'application Tailscale, connectée au même tailnet.</p>
<p align="center"><strong>Français</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Autres langues</a></p>

```text
# Claude Code, après avoir installé la CLI (voir Installation ci-dessous)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Clients MCP locaux qui utilisent du JSON mcpServers (autres clients : voir la configuration MCP de votre client)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Dites à votre agent : « Partage le serveur de développement pour que je puisse l'ouvrir sur mon téléphone. »
```

La même chose à la main, avec le serveur de développement déjà à l'écoute sur le port 5173. Avec `--json`, `share` indique le nom enregistré dans `data.name`. Utilisez ce nom dans les commandes suivantes : il diffère de `--name` quand ce nom était déjà pris, par exemple `web-2`.

```sh
tslink share 5173 --name web --json   # data.name ; data.url, ou data.auth_url (lien de connexion Tailscale) quand status vaut needs_login
tslink url <name> --wait              # une fois la connexion terminée : l'URL exacte
tslink remove <name>                  # quand vous avez fini ; à omettre si share a échoué. Le serveur de développement continue de tourner
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

<a id="agents"></a>

## Pour les agents IA

Un serveur de développement qu'un agent lance sur `localhost` est hors de portée de votre téléphone. TSLink permet à l'agent de lui donner une adresse privée, de vous remettre l'URL exacte et de retirer le partage une fois terminé. Votre téléphone a besoin de l'application Tailscale, connectée au même tailnet.

Testé le 2026-10-22 avec Vite 8.3.5 et un iPhone 15 Pro : l'agent a partagé le serveur de développement via MCP, le téléphone a ouvert l'URL privée et la page s'est mise à jour à l'enregistrement, sans rechargement manuel.

Pour Claude Code, installez d'abord la CLI, puis exécutez :

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Le plugin inclut un [skill d'aperçu](../skills/tslink-preview/SKILL.md) et la connexion MCP ci-dessous. Le marketplace reste dans ce dépôt et récupère le plugin depuis [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Le skill de référence reste ici ; le plugin en embarque une copie vérifiée par la CI. D'autres agents peuvent utiliser le même skill ou se connecter directement en MCP. Cette intégration exige la CLI locale et ne fonctionne pas dans le chat de claude.ai. [Configuration et inscription](agent-integrations.md)

Pour un client MCP local qui utilise du JSON `mcpServers` (autres clients : voir leurs instructions de configuration MCP et le [guide des clients MCP](mcp-clients.md)) :

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Passerelle par défaut pour les nouvelles installations.** Chaque application HTTP/fichier privée reçoit un port stable, par exemple `https://home.<tailnet>.ts.net:8443/`. Les installations existantes conservent leur mode et chaque application garde son nœud/URL ; `--portal-only` reste explicite.
- **Inscrivez la passerelle.** Sans identifiants, le compte propre vérifié de la passerelle sans tags peut devenir owner. Avec tags/OAuth, exécutez localement `tslink portal enable --owner <login>`. L’inscription en attente des nœuds applicatifs ne bloque pas une route de passerelle prête.
- **Autorisez les personnes.** Les nouvelles applications autorisent owner/admin et les personnes explicitement désignées. `people add`, `add --allow` et MCP `add/share.allow` vérifient l’appartenance et préparent les invitations nécessaires. Les membres n’ont pas besoin d’invitation de machine ; les personnes externes acceptent une invitation de passerelle. Les erreurs de répertoire/API renvoient un état incomplet.
- **Vous modifiez, vous enregistrez, vous voyez.** Enregistrer le code met à jour la page sur votre téléphone. Les règles Host et Origin de votre application peuvent encore bloquer des requêtes : lors de notre test, Vite n'a pas eu besoin de changer `allowedHosts` ; Next.js n'a pas été testé et exige toujours `allowedDevOrigins`.
- **Nettoyage.** L'agent appelle `unshare`, ou vous lancez `tslink remove <name>`, toujours avec le nom renvoyé par le résultat de share. La suppression désenregistre le partage et tente de supprimer son nœud du tailnet quand TSLink peut prouver qu'il en est propriétaire ; vérifiez donc les champs de nettoyage ou d'état conservé dans le résultat. Elle n'arrête pas le serveur de développement.
- **CLI ou MCP.** Les commandes de gestion acceptent `--json` et renvoient des résultats versionnés ; `tslink mcp` propose les opérations sur les applications et les accès via MCP.
- **Un fichier, pas le dossier.** Un agent peut partager uniquement son rapport HTML autonome avec `tslink share ./report.html` ; les autres fichiers de ce dossier restent inaccessibles.
- **Rôles limités.** `viewer`, `app-operator` ou `people-manager`, restreints aux applications que vous désignez ; partager une nouvelle application exige le rôle `owner`. `tslink mcp-audit` montre ce qu'un agent a modifié. Les rôles limitent les outils de TSLink, pas le shell de l'agent lui-même.

[Guide rapide des agents](agent-quickstart.md) · [Droits MCP](mcp-scopes.md) · [MCP distant](remote-mcp.md)

<a id="why"></a>

## Quand vous avez besoin de TSLink

Serve suffit pour une application que vous lancez vous-même et ouvrez sur votre téléphone. TSLink s'adresse aux serveurs de développement lancés par un agent, souvent plusieurs, quand chacun doit avoir son propre nom privé et que le partage, l'URL et le nettoyage doivent être prêts à l'emploi dans la CLI, MCP et un skill Claude Code.

| Besoin | Tailscale seul | TSLink |
|---|---|---|
| Plusieurs serveurs de développement lancés par votre agent, un nom chacun | Serve définit des routes au niveau de l'appareil sous un seul nom par machine ; Services demande pour chaque nom une définition, une approbation et un hôte étiqueté | Un nœud par application, avec partage, recherche de l'URL et nettoyage intégrés à la CLI (`share`, `url`, `remove`), à MCP (`share`, `url`, `unshare`) et à un skill |
| Une application que vous lancez vous-même, sur votre téléphone | `tailscale serve 3000` suffit | `tslink share 3000` |
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
