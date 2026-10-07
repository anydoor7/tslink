<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Endereços privados para seus apps, na sua rede Tailscale.</strong></p>
<p align="center">Abra-os nos seus próprios dispositivos. Compartilhe um com uma pessoa ou por link, até a data que você escolher.</p>
<p align="center"><strong>Português (Brasil)</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Mais idiomas</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalação

```sh
brew install --cask anydoor7/tap/tslink
```

Pacotes Linux `.deb` e `.rpm` e builds para Windows estão na [versão mais recente](https://github.com/anydoor7/tslink/releases/latest). Na primeira vez que você compartilha um app, o TSLink mostra um link de login do Tailscale para ele. [Primeiros passos](getting-started.md)

<a id="why"></a>

## Quando você precisa do TSLink

Para um app nos seus próprios dispositivos, o Serve basta. O TSLink junta endereços de apps, prazos e mudanças de acesso em um só fluxo.

| Tarefa | Só Tailscale | TSLink |
|---|---|---|
| Um app web no celular | `tailscale serve 3000` basta | `tslink share 3000` |
| Vários apps, cada um com seu nome | Configurar Services, ou nós separados | Um `share`/`add` por app; registrar cada nó |
| Uma pessoa, um app, sete dias | Regras de política, depois uma ferramenta JIT ou remoção manual | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/arquivos) |
| Link no navegador, três dias | Funnel público; adicionar um controle de acesso e um desligamento agendado | `tslink guest create photos --for 3d --public --print-link` (só HTTP) |

Destinatários privados precisam do Tailscale. Links de convidado são públicos, podem ser repassados e funcionam como credenciais de acesso.

[Comparação completa](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Seus apps, nos seus próprios dispositivos

- **Um endereço para cada app.** Apps web, pastas, arquivos avulsos e portas TCP ganham um nome próprio na sua tailnet, então você usa nomes em vez de endereços IP.
- **Privado por padrão.** Nada fica público até você criar um link de convidado ou publicar pelo Funnel.
- **Uma página inicial** que lista seus apps e o estado de cada um. [Portal](portal.md)
- **Verificações de saúde e alertas** por comando ou webhook, e um registro de acesso que inclui as solicitações negadas. [Saúde e alertas](health-and-alerts.md) · [Histórico de acesso](access-log.md)
- **Receitas para 15 apps auto-hospedados**, incluindo Home Assistant, Jellyfin, Immich e Ollama. `tslink apps detect` encontra os que já estão rodando. [Receitas de apps](apps.md)

## Compartilhe quando quiser

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Links de convidado e novas URLs públicas expiram e só funcionam para apps web; pastas, arquivos e portas TCP continuam privados. [Pessoas](people.md) · [Links de convidado](guest-links.md) · [Acesso público](funnel.md)

<a id="agents"></a>

## Para agentes de IA

Um servidor de desenvolvimento que um agente inicia em `localhost` fica fora do alcance do seu celular. O TSLink deixa o agente dar a ele um endereço privado, informar a URL exata e removê-lo ao terminar.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI ou MCP.** Os comandos de gerenciamento aceitam `--json` e retornam resultados versionados; `tslink mcp` oferece operações de apps e acesso via MCP.
- **Papéis limitados.** `viewer`, `app-operator` ou `people-manager`, restritos aos apps que você indicar. `tslink mcp-audit` mostra o que um agente mudou. Os papéis limitam as ferramentas do TSLink, não o próprio shell do agente.

[Guia de agentes](agent-quickstart.md) · [Permissões MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

<a id="architecture"></a>

## Como funciona

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Um PC ou servidor na nuvem: CLI/MCP gerencia um daemon comum e nós por app. Dispositivos privados usam Tailscale criptografado; HTTPS/Funnel público opcional chega aos apps HTTP por controle de convidados ou publicação aberta explícita." width="960">
</picture>

Um processo em segundo plano roda um nó Tailscale separado para cada app. O Tailscale fornece o transporte da tailnet e os certificados HTTPS. O acesso privado a apps web e arquivos pode ser limitado por identidade Tailscale com `--allow` e acessos por pessoa; o TCP puro depende da política da sua tailnet e do login do próprio app. [Arquitetura](architecture.md)

<a id="requirements"></a>

## Requisitos

| Quem | Precisa de |
|---|---|
| Você | Uma conta Tailscale com MagicDNS e HTTPS ativados |
| A máquina que roda seus apps | O TSLink, que já inclui o Tailscale (no Linux, uma sessão de usuário do systemd) |
| Seus dispositivos e as pessoas com quem você compartilha | O app do Tailscale |
| Convidados | Um navegador |

Os nomes dos apps HTTPS aparecem em registros públicos de certificados, então escolha nomes que você não se importe que outras pessoas vejam.

<a id="documentation"></a>

## Mais

[Toda a documentação](INDEX.md) · [Referência CLI](cli-reference.md) · [Comparação com Serve, ngrok e Cloudflare](comparison.md) · [Contribuir](../CONTRIBUTING.md) · [Segurança](../SECURITY.md)

Apache 2.0. O TSLink é um projeto independente, não criado nem endossado pelo Tailscale.
