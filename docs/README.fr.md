<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Accédez à vos applications et gérez-les où que vous soyez.<br>Gardez-les privées ou partagez-les à vos conditions.</strong></p>

Vos applications, sur votre ordinateur ou serveur cloud : accédez-y par un réseau privé chiffré, ou choisissez des liens invités pour navigateur ou un accès public. Gérez-les vous-même ou avec un agent.

<p align="center"><a href="#quickstart">Démarrage rapide</a> · <a href="#agents">Pour les agents</a> · <a href="#documentation">Documentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <strong>Français</strong> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Vos applications à portée de main

| Votre besoin | Ce que propose TSLink |
|---|---|
| Utiliser vos applications sur plusieurs appareils | Des adresses privées pour tableaux de bord domestiques, pages web locales, fichiers, API de modèles et services TCP sur PC ou serveur. |
| Partager avec des personnes précises | Applications HTTP/fichiers sélectionnées, identité Tailscale vérifiée, expiration et révocation. Les destinataires utilisent Tailscale. [Personnes](people.md) |
| Accueillir quelqu'un dans son navigateur | Liens invités temporaires avec PIN facultatif pour applications proxy HTTP, ou HTTPS explicitement public via Funnel. Un lien peut être transmis et ne prouve pas l'identité. [Liens invités](guest-links.md) |
| Gérer un ensemble d'applications | Inventaire par hôte, portail privé, contrôles de santé et alertes, historique d'accès et gestion CLI/MCP avec rôles d'agents, périmètres applicatifs et traces d'audit. [Portail](portal.md) · [Droits MCP](mcp-scopes.md) |

[Recettes d'applications](apps.md), [limites d'envoi](sharing.md), [durées flexibles](durations.md) et [accueil par QR et demandes d'accès](requests.md) facilitent le quotidien. Ces fonctions sont incluses dans ce code source.

<a id="installation"></a>
<a id="quickstart"></a>

## Démarrage rapide

Installez depuis les sources avec **Git et Go 1.26.6+** ; les versions précompilées et Homebrew ne sont pas encore publiées. Les commandes utilisent bash/zsh. [Configuration macOS, Linux et Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Il faut l'accès au dépôt, un **compte Tailscale** et [MagicDNS et HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Les appareils privés destinataires ont besoin de Tailscale et d'une autorisation réseau. TSLink embarque Tailscale sur l'hôte des applications.

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
