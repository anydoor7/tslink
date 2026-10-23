<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">O TSLink é uma CLI e um servidor MCP de código aberto que dá a cada app do seu computador um endereço privado <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, para que você ou seu coding agent possam abrir no celular um servidor de desenvolvimento em execução. Projeto independente, não feito pela Tailscale.</p>
<p align="center">Seu celular precisa do app Tailscale, conectado à mesma tailnet.</p>
<p align="center"><strong>Português (Brasil)</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Mais idiomas</a></p>

```text
# Claude Code, depois de instalar a CLI (veja Instalação abaixo)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Clientes MCP locais que usam JSON com mcpServers (outros: veja a configuração de MCP do seu cliente)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Peça ao seu agente: "Compartilhe o servidor de desenvolvimento para eu abrir no celular."
```

A mesma coisa, feita à mão, com o servidor de desenvolvimento já escutando na porta 5173. Com `--json`, o `share` informa o nome registrado em `data.name`. Use esse nome nos comandos seguintes: ele difere de `--name` quando esse nome já estava em uso, por exemplo `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, ou data.auth_url (um link de login do Tailscale) quando status é needs_login
tslink url <name> --wait              # depois de concluir o login: a URL exata
tslink remove <name>                  # quando terminar; pule se o share falhou. O servidor de desenvolvimento continua rodando
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalação

Máquinas macOS exigem macOS 13 Ventura ou posterior ([plataformas compatíveis](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

No Windows, instale com Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Para atualizar, execute `brew upgrade --cask tslink` ou `scoop update; scoop update tslink`. Se o TSLink estiver rodando como serviço em segundo plano, execute `tslink install` novamente depois.

Pacotes Linux `.deb` e `.rpm` e builds para Windows estão na [versão mais recente](https://github.com/anydoor7/tslink/releases/latest). Na primeira vez que você compartilha um app, o TSLink mostra um link de login do Tailscale para ele. [Primeiros passos](getting-started.md)

<a id="agents"></a>

## Para agentes de IA

Um servidor de desenvolvimento que um agente inicia em `localhost` fica fora do alcance do seu celular. O TSLink deixa o agente dar a ele um endereço privado, entregar a você a URL exata e remover o compartilhamento ao terminar. Seu celular precisa do app Tailscale, conectado à mesma tailnet.

Testado em 2026-10-22 com Vite 8.3.5 e um iPhone 15 Pro: o agente compartilhou o servidor de desenvolvimento por MCP, o celular abriu a URL privada e a página foi atualizada ao salvar, sem recarregar manualmente.

Para o Claude Code, instale primeiro a CLI e depois execute:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

O plugin inclui uma [skill de pré-visualização](../skills/tslink-preview/SKILL.md) e a conexão MCP abaixo. O marketplace fica neste repositório e busca o plugin em [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). A skill de referência fica aqui; o plugin leva uma cópia verificada pela CI. Outros agentes podem usar a mesma skill ou conectar o MCP diretamente. Esta integração exige a CLI local e não funciona no chat do claude.ai. [Configuração e cadastro](agent-integrations.md)

Para um cliente MCP local que usa JSON com `mcpServers` (outros clientes: veja as instruções de configuração de MCP deles e o [guia de clientes MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Um nome por servidor de desenvolvimento.** Cada compartilhamento recebe seu próprio endereço privado `https://<name>.<tailnet>.ts.net`, no caminho raiz e na porta 443. Se o nome já estiver em uso, ganha um sufixo numérico; por isso, use a URL que o agente recebe de volta.
- **Cada app novo precisa de uma pessoa.** Na configuração padrão, cada novo nó de app exige que uma pessoa conclua o cadastro no Tailscale. Se o resultado for `needs_login`, conclua o login e depois execute `tslink url <name> --wait` para obter a URL exata. À parte, um nome novo pode levar um tempo para ficar acessível: de 36 a 38 segundos nas nossas duas execuções de teste.
- **Quem pode abrir.** Por padrão, os dispositivos da tailnet que a sua política permite. O `share` do MCP aceita `allow` para limitar ao seu login; pela CLI, use `tslink add web --proxy localhost:5173 --allow you@example.com`. Os nomes aparecem nos registros públicos de certificados, então não são secretos.
- **Edita, salva, vê.** Ao salvar o código, a página é atualizada no seu celular. As regras de Host e Origin do seu próprio app ainda podem bloquear requisições: no nosso teste o Vite não precisou mudar `allowedHosts`; o Next.js não foi testado e ainda exige `allowedDevOrigins`.
- **Limpeza.** O agente chama `unshare`, ou você executa `tslink remove <name>`, sempre com o nome que o resultado do share devolveu. A remoção cancela o registro do compartilhamento e tenta excluir o nó dele na tailnet quando o TSLink consegue provar que é o dono; por isso, confira no resultado os campos de limpeza ou de estado retido. Ela não para o servidor de desenvolvimento.
- **CLI ou MCP.** Os comandos de gerenciamento aceitam `--json` e retornam resultados versionados; `tslink mcp` oferece operações de apps e acesso via MCP.
- **Um arquivo, não a pasta.** Um agente pode compartilhar só o próprio relatório HTML autocontido com `tslink share ./report.html`; os outros arquivos da pasta continuam inacessíveis.
- **Papéis limitados.** `viewer`, `app-operator` ou `people-manager`, restritos aos apps que você indicar; compartilhar um app novo exige o papel `owner`. `tslink mcp-audit` mostra o que um agente mudou. Os papéis limitam as ferramentas do TSLink, não o próprio shell do agente.

[Guia de agentes](agent-quickstart.md) · [Permissões MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

<a id="why"></a>

## Quando você precisa do TSLink

O Serve basta para um app que você mesmo inicia e abre no seu celular. O TSLink é para os servidores de desenvolvimento que um agente inicia, muitas vezes vários, quando cada um deve ter seu próprio nome privado e os passos de compartilhar, obter a URL e limpar já vêm prontos na CLI, no MCP e em uma skill do Claude Code.

| Tarefa | Só Tailscale | TSLink |
|---|---|---|
| Vários servidores de desenvolvimento que seu agente inicia, um nome para cada | O Serve define rotas no nível do dispositivo sob um nome por máquina; o Services exige, para cada nome, uma definição, uma aprovação e um host com tags | Um nó por app, com compartilhamento, consulta da URL e limpeza prontos na CLI (`share`, `url`, `remove`), no MCP (`share`, `url`, `unshare`) e em uma skill |
| Um app que você mesmo inicia, no celular | `tailscale serve 3000` basta | `tslink share 3000` |
| Uma pessoa, um app, sete dias | Regras de política, depois uma ferramenta JIT ou remoção manual | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/arquivos) |
| Link no navegador, três dias | Funnel público; adicionar um controle de acesso e um desligamento agendado | `tslink guest create photos --for 3d --public --print-link` (só HTTP) |

Destinatários privados precisam do Tailscale. Links de convidado são públicos, podem ser repassados e funcionam como credenciais de acesso.

[Comparação completa](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Seus apps, nos seus próprios dispositivos

- **Um endereço para cada app.** Apps web, pastas, arquivos avulsos e portas TCP ganham um nome próprio na sua tailnet, então você usa nomes em vez de endereços IP.
- **Privado por padrão.** Nada fica público até você criar um link de convidado ou publicar pelo Funnel.
- **Um app, não a máquina inteira.** Cada app que você publica ganha um nó próprio, que encaminha só para esse app. Sem o app do Tailscale no host, o TSLink não adiciona outras portas do host à sua tailnet.
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
