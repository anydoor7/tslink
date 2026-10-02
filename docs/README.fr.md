<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo de TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Donnez à vos applications, modèles et fichiers locaux leur propre adresse privée.</strong><br>
  Ouvrez-les depuis un autre appareil autorisé de votre réseau Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licence : Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 ou version ultérieure"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale : nœuds tsnet intégrés"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP : 19 outils"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <strong>Français</strong> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installation

Il vous faut **Go 1.26.6 ou une version ultérieure** et Git. Aucune version précompilée ni aucun cask Homebrew n'a encore été publié ; installez depuis le code source. Ces exemples utilisent **bash ou zsh** ; consultez la [prise en charge des plateformes](platforms.md) pour les prérequis de Windows et des services en arrière-plan.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Utilisez un compte Tailscale avec [MagicDNS et HTTPS activés](https://tailscale.com/docs/how-to/set-up-https-certificates). L'appareil destinataire doit être connecté à votre réseau Tailscale (**tailnet**), dont la politique doit autoriser l'accès au service. TSLink intègre Tailscale sur la machine qui publie.

### Partager votre première page

Créez une page ; TSLink la sert directement et démarre son service en arrière-plan si nécessaire :

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Si TSLink affiche une URL d'inscription, ouvrez-la pour autoriser le nœud ; votre tailnet peut également exiger une approbation de l'appareil par un administrateur. Récupérez ensuite l'adresse exacte :

```bash
tslink url demo --wait
```

Ouvrez cette URL sur un appareil autorisé. Aucun jeton API n'est nécessaire pour ce premier partage.
[Configuration complète et détails du cycle de vie →](getting-started.md)

<a id="use-cases"></a>

## Que souhaitez-vous partager ?

Les fichiers doivent exister ; les applications, bases de données et backends de modèles doivent déjà fonctionner sur les ports indiqués.

| Usage | Commande |
|---|---|
| Ouvrir une application locale depuis un autre appareil | `tslink share 3000` |
| Parcourir un répertoire de fichiers | `tslink share ./public --name files` |
| Lire un rapport HTML généré sur votre téléphone | `tslink share ./report.html --name report` |
| Se connecter à une base de données locale via TCP | `tslink add database --tcp localhost:5432` |
| Utiliser une API HTTP de modèle local, comme Ollama | `tslink add model --proxy localhost:11434` |

Pour Ollama, récupérez l'URL exacte avec `tslink url model --wait` ; le `baseURL` d'un client compatible OpenAI reprend cette URL suivie de `/v1`. [Modèles locaux et usages de données privées →](local-ai.md)

Pour plusieurs applications sur un hôte, TSLink réunit des nœuds nommés, des listes d’identités autorisées pour HTTP, l’expiration de Funnel et la gestion MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) peut suffire pour une application sur vos propres appareils.

<a id="architecture"></a>

## Architecture

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Exemple de schéma des services : App, Docs, Database et Model sont des nœuds nommés distincts dans une même tailnet. Les applications, fichiers et API de modèles utilisent HTTPS ; la base de données utilise TCP privé." width="960">
</picture>

**Une tailnet, des nœuds de service distincts.** Un daemon partagé exécute un nœud tsnet intégré par service pour transmettre HTTP, servir des fichiers ou relayer TCP. Les modifications du registre prennent effet pendant son fonctionnement. Chaque nœud possède sa propre identité réseau ; les services partagent la machine qui les publie.
[Détails de l'architecture →](architecture.md)

| Composant | Rôle |
|---|---|
| [Go](../go.mod) | Binaire natif en ligne de commande |
| [Tailscale tsnet](architecture.md) | Nœuds de service et transport de la tailnet |
| [Cobra](https://github.com/spf13/cobra) | Commandes et aide |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transports pour les agents |
| Trousseau du système et gestionnaire de services utilisateur | Identifiants facultatifs et fonctionnement en arrière-plan |

Les services restent dans votre tailnet sauf si vous activez explicitement [Funnel public](getting-started.md#more-examples). Les services HTTP et de fichiers prennent en charge des listes d'identités autorisées (`WhoIs`, `--allow`) ; TCP utilise la politique de la tailnet et l'authentification propre au backend. Consultez les [limites du partage](sharing.md).

TSLink n’installe pas les applications, n’exécute pas les modèles, n’isole pas les processus de l’hôte et ne regroupe pas plusieurs hôtes. Le réseau, le chiffrement et HTTPS viennent de Tailscale ; TSLink est un projet indépendant.

<a id="agents"></a>

## Pour les agents

Les **19 outils MCP** permettent à un agent de partager des rapports, gérer les services, récupérer les URL et inspecter la configuration. Connectez un client MCP local au binaire installé :

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

MCP gère TSLink ; les applications utilisent l'API HTTP de modèles pour l'inférence. Consultez les [clients MCP](mcp-clients.md), le [MCP distant](remote-mcp.md) et le [guide d'exploitation pour les agents](../AGENTS.md) pour la configuration et l'automatisation.

L’automatisation CLI utilise `--json` avec `schema_version` à `1` ; consultez `tslink status --urls --json`. MCP local utilise JSON-RPC sur stdio. Voir [automatisation JSON](json-automation.md).

<a id="roadmap"></a>

## À venir

Les éléments En cours de fusion, En cours de revue ou Prévu ne sont pas inclus dans l’installation depuis les sources ci-dessus.

| Cas d’usage | État |
|---|---|
| <!-- roadmap:people --> Donnez à un proche 3 jours d’accès aux applications HTTP/fichiers privées, avec leurs invitations dans un message ; le destinataire a toujours besoin de Tailscale. | En cours de fusion |
| <!-- roadmap:health --> Vérifiez l’état des applications et recevez des alertes de panne ou d’expiration par une commande ou un webhook facultatif. | En cours de fusion |
| <!-- roadmap:recipes --> Détectez les applications compatibles sur loopback et prévisualisez les recettes d’applications auto-hébergées avant de les partager. | En cours de fusion |
| <!-- roadmap:limits --> Réglez la taille des téléversements et les délais des requêtes de chaque application HTTP pour les gros fichiers et les clients lents. | En cours de fusion |
| <!-- roadmap:windows --> Redémarrez un daemon Windows après un plantage pendant une session ouverte, avec une tâche planifiée et un superviseur intégré. | En cours de fusion |
| <!-- roadmap:access-log --> Consultez qui a ouvert quelle application dans les journaux locaux, avec les modes de chemin `prefix`, `full` ou `off`. | En cours de revue |
| <!-- roadmap:portal --> Ouvrez une page d’accueil listant les applications autorisées et les liens d’inscription pour les propriétaires ; les visiteurs ont toujours besoin de Tailscale. | En cours de revue |
| <!-- roadmap:mcp-scopes --> Attribuez à un agent un rôle et un périmètre d’applications, avec des reçus d’audit pour ses modifications. | En cours de revue |
| <!-- roadmap:guest-links --> Laissez un invité ouvrir une application HTTP dans son navigateur sans installer Tailscale, avec un lien temporaire et un PIN facultatif, via Funnel public avec contrôle d’accès. | En cours de revue |
| <!-- roadmap:durations --> Choisissez des durées prédéfinies ou personnalisées d’au moins 1 heure, avec un maximum invité de 7 jours par défaut, configurable. | En cours de revue |
| <!-- roadmap:requests --> Aidez les utilisateurs de téléphones à rejoindre via un QR code ; permettez au propriétaire d’approuver les demandes d’accès aux applications ou de temps supplémentaire en une action. | En cours de revue |
| <!-- roadmap:multi-host --> Consultez les applications de plusieurs hôtes dans un inventaire. | Prévu |

<a id="documentation"></a>

## Documentation et licence

[Premiers pas](getting-started.md) · [Modèles locaux](local-ai.md) ·
[Référence CLI](cli-reference.md) · [Plateformes](platforms.md) · [Feuille de route](roadmap.md)

Contribuez en suivant [CONTRIBUTING.md](../CONTRIBUTING.md) ; signalez les vulnérabilités selon [SECURITY.md](../SECURITY.md).

TSLink utilise la [licence Apache 2.0](../LICENSE) sans modification, y compris pour un usage commercial. Conservez les mentions [NOTICE](../NOTICE) applicables et les [avis de tiers](../THIRD_PARTY_NOTICES.md) lors de la redistribution. La [coopération commerciale](../COMMERCIAL.md) est volontaire et n'ajoute aucune condition de licence. Les conditions de service et les offres de Tailscale s'appliquent séparément.
