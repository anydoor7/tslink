<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logotipo do TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Compartilhe os apps do seu computador com quem você escolher, pelo tempo que você decidir.</strong><br>
  Cada app recebe seu próprio endereço privado na sua rede Tailscale. Veja quem tem acesso e retire a permissão.
</p>

<p align="center">
  <a href="#quickstart">Início rápido</a> · <a href="#agents">Para agentes</a> · <a href="getting-started.md">Documentação</a> ·
  <strong>Português (Brasil)</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Todos os idiomas</a>
</p>

## Para que as pessoas usam

- **Abra seu trabalho no celular.** Um relatório gerado por um script, um servidor de desenvolvimento, um notebook ou uma API de modelo local, em um endereço HTTPS privado acessível por dispositivos autorizados.
- **Dê a uma pessoa acesso temporário a um app.** Deixe seu parceiro usar a biblioteca de fotos por uma semana ou um colega testar uma prévia por três dias. O acesso expira sozinho; você também pode encerrá-lo antes.
- **Deixe seu agente cuidar do compartilhamento.** Seu agente de programação acabou de criar um painel. Peça para compartilhá-lo com você e seu colega até sexta-feira. Ele também pode informar o que está compartilhado e retirar um acesso.

Seus apps continuam rodando onde já estavam. O TSLink controla quem pode acessar cada um e mantém uma lista do que é compartilhado, com quem e até quando.

<a id="quickstart"></a>

## Início rápido

Você precisa de **Go 1.26.6+**, Git e uma conta Tailscale com [MagicDNS e HTTPS ativados](https://tailscale.com/docs/how-to/set-up-https-certificates). Ainda não há versões pré-compiladas publicadas, então instale a partir do código-fonte:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Compartilhe uma página:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Na primeira vez, o TSLink mostra um link de login para registrar o novo nó de serviço; sua tailnet também pode exigir a aprovação do dispositivo por um administrador. Após o registro, abra a URL do serviço em um dispositivo autorizado conectado à sua tailnet. Não é necessário um token de API.

Confira os compartilhamentos e remova a demonstração:

```bash
tslink status --urls
tslink remove demo
```

Você também pode compartilhar estes itens quando o backend estiver rodando:

| Conteúdo | Comando |
|---|---|
| Um app web local | `tslink share 3000` |
| Uma pasta de arquivos | `tslink share ./public --name files` |
| Uma API de modelo local, como Ollama | `tslink add model --proxy localhost:11434` |
| Um banco de dados por TCP privado | `tslink add database --tcp localhost:5432` |
| Um app auto-hospedado conhecido (Jellyfin, Immich, Home Assistant e outros 13) | `tslink apps detect`, depois `tslink apps share jellyfin --yes` |

[Primeiros passos, plataformas e serviço em segundo plano →](getting-started.md)

## Escolha quem pode acessar

| Público | O que o destinatário precisa | Identidade | Fim do acesso |
|---|---|---|---|
| **Seus dispositivos** | Login na sua tailnet | Identidade Tailscale verificada | Quando você remove o app |
| **Pessoas específicas** (HTTP/arquivos privados) | Login Tailscale; pessoas de fora aceitam um convite por app | Identidade Tailscale verificada | No prazo definido (`--for 7d`) ou com `tslink people remove` |
| **Qualquer pessoa com a URL** (Funnel) | Um navegador | Qualquer pessoa; o login do próprio app continua valendo | Após 24 horas por padrão (`--funnel-ttl`) |
| **Link de visitante pelo navegador** *(em breve)* | Um navegador e um PIN opcional | Quem tiver o link | No prazo do link ou na revogação |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Os prazos de HTTP e arquivos privados são verificados a cada requisição. Revogar o acesso bloqueia novas requisições; não recupera dados baixados nem fecha fluxos ou conexões WebSocket já aceitos. [Compartilhar com pessoas →](people.md) · [Limites do compartilhamento →](sharing.md)

<a id="agents"></a>

## Para agentes

O TSLink inclui um servidor MCP para que um agente compartilhe, liste, explique e remova compartilhamentos como você. Adicione-o a um cliente MCP local:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Resultados exatos.** A automação CLI aceita `--json` com `schema_version: 1` e códigos de erro estáveis; `tslink mcp` usa JSON-RPC. `tslink manifest` descreve cada comando e opção. Os agentes devem obter URLs reais com `tslink url <name> --wait` em vez de montá-las.
- **Espera informada com clareza.** Um novo nó que ainda precisa de login humano informa `needs_login`, sem fingir que está pronto.
- **Permissões.** O MCP local roda com as permissões do seu usuário. O MCP remoto exige ativação explícita, é acessível apenas na tailnet e se limita às identidades ou tags indicadas. Papéis por agente, escopos de apps e comprovantes de ações chegam *em breve*.

O MCP do TSLink opera o próprio TSLink. Se você publicar outro servidor MCP pelo TSLink, esse servidor ainda precisará das suas próprias permissões de ferramentas.
[Guia para agentes →](agents.md) · [Clientes MCP →](mcp-clients.md) · [MCP remoto →](remote-mcp.md) · [Automação JSON →](json-automation.md)

## Quando usar outra ferramenta

| Se você quer | Considere |
|---|---|
| Um serviço local nos seus dispositivos, com o cliente Tailscale que já utiliza | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Serviços administrados com nomes estáveis em vários hosts | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Uma URL pública para um webhook ou demonstração de API sem conta Tailscale | [ngrok](https://ngrok.com/docs/start) ou [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Instalar e executar apps auto-hospedados, além de compartilhá-los | [Umbrel](https://umbrel.com) ou [Coolify](https://coolify.io) |
| Uma plataforma de acesso baseada em identidade para toda a organização | [Pangolin](https://github.com/fosrl/pangolin) ou [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

O TSLink é adequado quando uma pessoa executa vários apps e quer acesso temporário por app e por pessoa, que ela e seu agente possam consultar.

## Como funciona

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database e Model são nós separados com nomes próprios em uma tailnet, executados por um único daemon TSLink no computador que publica os serviços." width="720">
</picture>

Um daemon em segundo plano executa um nó Tailscale integrado por app, dando a cada um seu próprio nome e endereço. Para HTTP e arquivos privados, `WhoIs` e permissões por pessoa ou regras `--allow` controlam o acesso; os prazos são verificados a cada requisição. TCP bruto usa a política da tailnet e a autenticação do backend. O Tailscale fornece transporte da tailnet, criptografia e certificados; o TSLink é um projeto independente. Todos os apps compartilham o computador que os publica; o TSLink não os isola uns dos outros. [Arquitetura →](architecture.md)

## Estado

Disponível agora: endereços privados por app, pessoas com prazos e pacotes de convites, Funnel público com expiração, verificações de saúde e alertas, receitas de apps auto-hospedados, limites de requisições por app, reinício após falhas no Windows, CLI e MCP. Também disponíveis: links de visitantes pelo navegador, durações flexíveis, registro de acesso, página inicial dos apps, papéis limitados de agentes, integração por QR e solicitações de acesso.

Uma lista única para vários computadores está planejada. [Plano de desenvolvimento →](roadmap.md)

## Documentação e licença

[Primeiros passos](getting-started.md) · [Referência CLI](cli-reference.md) · [Plataformas](platforms.md) · [Modelos locais](local-ai.md) · [Contribuições](../CONTRIBUTING.md) · [Segurança](../SECURITY.md)

Apache License 2.0, incluindo uso comercial. Mantenha [NOTICE](../NOTICE) e os [avisos de terceiros](../THIRD_PARTY_NOTICES.md) ao redistribuir. Os termos e planos do Tailscale se aplicam separadamente.
