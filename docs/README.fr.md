<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Partagez les applications de votre ordinateur avec les personnes de votre choix, pour la durée que vous choisissez.</strong><br>
  Chaque application possède sa propre adresse privée sur votre réseau Tailscale. Consultez les accès et retirez-les quand vous le souhaitez.
</p>

<p align="center">
  <a href="#quickstart">Démarrage rapide</a> · <a href="#agents">Pour les agents</a> · <a href="getting-started.md">Documentation</a> ·
  <strong>Français</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Toutes les langues</a>
</p>

## À quoi sert TSLink

- **Retrouvez votre travail sur votre téléphone.** Un rapport généré par un script, un serveur de développement, un notebook ou une API de modèle local, à une adresse HTTPS privée accessible aux appareils autorisés.
- **Donnez temporairement accès à une application à une personne.** Votre partenaire peut utiliser la photothèque pendant une semaine, ou un collègue tester votre aperçu pendant trois jours. L'accès expire automatiquement ; vous pouvez aussi l'arrêter plus tôt.
- **Confiez le partage à votre agent.** Votre agent de programmation vient de créer un tableau de bord. Demandez-lui de le partager avec vous et votre collègue jusqu'à vendredi. Il peut aussi vous indiquer ce qui est partagé et retirer un accès.

Vos applications continuent de fonctionner là où elles se trouvent. TSLink contrôle qui peut accéder à chacune et tient une liste de ce qui est partagé, avec qui et jusqu'à quand.

<a id="quickstart"></a>

## Démarrage rapide

Il vous faut **Go 1.26.6+**, Git et un compte Tailscale avec [MagicDNS et HTTPS activés](https://tailscale.com/docs/how-to/set-up-https-certificates). Aucune version précompilée n'est encore publiée ; installez depuis les sources :

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Partagez une page :

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Au premier lancement, TSLink affiche un lien de connexion pour inscrire le nouveau nœud de service ; votre tailnet peut aussi exiger l'approbation de l'appareil par un administrateur. Après l'inscription, ouvrez l'URL du service sur un appareil autorisé connecté à votre tailnet. Aucun jeton API n'est nécessaire.

Vérifiez les partages, puis supprimez la démonstration :

```bash
tslink status --urls
tslink remove demo
```

Voici d'autres éléments à partager une fois leur backend démarré :

| Élément | Commande |
|---|---|
| Une application web locale | `tslink share 3000` |
| Un dossier de fichiers | `tslink share ./public --name files` |
| Une API de modèle local, telle qu'Ollama | `tslink add model --proxy localhost:11434` |
| Une base de données en TCP privé | `tslink add database --tcp localhost:5432` |
| Une application auto-hébergée connue (Jellyfin, Immich, Home Assistant et 13 autres) | `tslink apps detect`, puis `tslink apps share jellyfin --yes` |

[Premiers pas, plateformes et service en arrière-plan →](getting-started.md)

## Choisissez qui peut accéder

| Public | Ce qu'il faut au destinataire | Identité | Fin de l'accès |
|---|---|---|---|
| **Vos propres appareils** | Connexion à votre tailnet | Identité Tailscale vérifiée | Lorsque vous supprimez l'application |
| **Personnes nommées** (HTTP/fichiers privés) | Un compte Tailscale ; les personnes extérieures acceptent une invitation par application | Identité Tailscale vérifiée | À l'échéance fixée (`--for 7d`) ou avec `tslink people remove` |
| **Toute personne disposant de l'URL** (Funnel) | Un navigateur | N'importe qui ; la connexion propre à l'application reste applicable | Après 24 heures par défaut (`--funnel-ttl`) |
| **Lien invité pour navigateur** | Un navigateur et éventuellement un PIN | La personne qui détient le lien | À son échéance ou lors de sa révocation |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Pour les partages HTTP et de fichiers privés, les échéances sont vérifiées à chaque requête. La révocation bloque les nouvelles requêtes ; elle ne récupère pas les données téléchargées et ne ferme pas les flux ou connexions WebSocket déjà acceptés. [Partager avec des personnes →](people.md) · [Limites du partage →](sharing.md)

<a id="agents"></a>

## Pour les agents

TSLink inclut un serveur MCP : un agent peut partager, lister, expliquer et supprimer des partages comme vous. Ajoutez-le à un client MCP local :

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Résultats précis.** L'automatisation CLI prend en charge `--json` avec `schema_version: 1` et des codes d'erreur stables ; `tslink mcp` utilise plutôt JSON-RPC. `tslink manifest` décrit toutes les commandes et options. Les agents doivent récupérer les URL réelles avec `tslink url <name> --wait`, plutôt que les construire.
- **Attente signalée honnêtement.** Un nouveau nœud nécessitant encore une connexion humaine indique `needs_login` au lieu de prétendre être prêt.
- **Droits.** MCP local s'exécute avec les droits de votre utilisateur. MCP distant est un point d'accès à activer explicitement, limité au tailnet et aux identités ou tags que vous indiquez. Les rôles par agent, périmètres d'applications et reçus d'actions sont disponibles.

Le MCP de TSLink pilote TSLink lui-même. Si vous publiez un autre serveur MCP via TSLink, celui-ci a toujours besoin de ses propres autorisations d'outils.
[Guide des agents →](agents.md) · [Clients MCP →](mcp-clients.md) · [MCP distant →](remote-mcp.md) · [Automatisation JSON →](json-automation.md)

## Quand choisir un autre outil

| Votre besoin | À envisager |
|---|---|
| Un service local sur vos appareils, avec le client Tailscale déjà en cours d'exécution | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Des services administrés avec des noms stables sur plusieurs hôtes | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Une URL publique pour un webhook ou une démo d'API, sans compte Tailscale | [ngrok](https://ngrok.com/docs/start) ou [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Installer et exécuter des applications auto-hébergées, au-delà du partage | [Umbrel](https://umbrel.com) ou [Coolify](https://coolify.io) |
| Une plateforme d'accès fondée sur l'identité à l'échelle de l'organisation | [Pangolin](https://github.com/fosrl/pangolin) ou [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink convient lorsqu'une personne exploite plusieurs applications et souhaite des accès temporaires par application et par personne, consultables par elle et son agent.

## Fonctionnement

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database et Model sont des nœuds distincts et nommés dans un même tailnet, exécutés par un seul daemon TSLink sur l'ordinateur qui publie les services." width="720">
</picture>

Un daemon en arrière-plan exécute un nœud Tailscale embarqué par application, chacune ayant ainsi son nom et son adresse. Pour HTTP et les fichiers privés, `WhoIs` et les autorisations par personne ou les règles `--allow` contrôlent l'accès ; les échéances sont vérifiées à chaque requête. Le TCP brut utilise la politique du tailnet et l'authentification du backend. Tailscale fournit le transport du tailnet, le chiffrement et les certificats ; TSLink est un projet indépendant. Toutes les applications partagent l'ordinateur de publication ; TSLink ne les isole pas entre elles. [Architecture →](architecture.md)

## État du projet

Disponible : adresses privées par application, personnes avec échéances et invitations groupées, Funnel public avec expiration, contrôles de santé et alertes, recettes d'applications auto-hébergées, limites de requêtes par application, redémarrage après plantage sous Windows, CLI et MCP. Également disponibles : liens invités pour navigateur, durées flexibles, journal d'accès, page d'accueil des applications, rôles d'agent limités, inscription par QR et demandes d'accès.

L'affichage de plusieurs ordinateurs dans une liste commune est prévu. [Feuille de route →](roadmap.md)

## Documentation et licence

[llms.txt](../llms.txt) · [Démarrage rapide pour les agents](agent-quickstart.md) · [Choisir un outil de partage](comparison.md)

[Premiers pas](getting-started.md) · [Référence CLI](cli-reference.md) · [Plateformes](platforms.md) · [Modèles locaux](local-ai.md) · [Contribuer](../CONTRIBUTING.md) · [Sécurité](../SECURITY.md)

Apache License 2.0, y compris pour un usage commercial. Conservez [NOTICE](../NOTICE) et les [mentions de tiers](../THIRD_PARTY_NOTICES.md) lors de la redistribution. Les conditions et offres de Tailscale s'appliquent séparément.
