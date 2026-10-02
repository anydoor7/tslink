<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo do TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Dê aos seus apps, modelos e arquivos locais um endereço privado próprio.</strong><br>
  Abra-os em outro dispositivo autorizado na sua rede Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licença: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 ou mais recente"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: nós tsnet incorporados"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 ferramentas"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <strong>Português (Brasil)</strong> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Instalação

Você precisa de **Go 1.26.6 ou mais recente** e Git. Ainda não foram publicadas versões pré-compiladas nem um cask do Homebrew; instale a partir do código-fonte. Estes exemplos usam **bash ou zsh**; consulte o [suporte a plataformas](platforms.md) para os requisitos do Windows e dos serviços em segundo plano.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Use uma conta Tailscale com [MagicDNS e HTTPS habilitados](https://tailscale.com/docs/how-to/set-up-https-certificates). O dispositivo que acessa o serviço deve estar conectado à sua rede Tailscale (**tailnet**), com permissão nas políticas da rede para alcançá-lo. O TSLink incorpora o Tailscale na máquina que publica.

### Compartilhe sua primeira página

Crie uma página; o TSLink a serve diretamente e inicia o serviço em segundo plano quando necessário:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Se o TSLink exibir uma URL de registro, abra-a para autorizar o nó; sua tailnet também pode exigir a aprovação do dispositivo por um administrador. Depois, obtenha o endereço exato:

```bash
tslink url demo --wait
```

Abra essa URL em um dispositivo autorizado. Não é necessário um token de API para este primeiro compartilhamento.
[Configuração completa e detalhes do ciclo de vida →](getting-started.md)

<a id="use-cases"></a>

## O que você vai compartilhar?

Os arquivos devem existir; apps, bancos de dados e backends de modelos já devem estar em execução nas portas indicadas.

| Caso de uso | Comando |
|---|---|
| Abrir um app local em outro dispositivo | `tslink share 3000` |
| Navegar por uma pasta de arquivos | `tslink share ./public --name files` |
| Ler um relatório HTML gerado no celular | `tslink share ./report.html --name report` |
| Conectar-se a um banco de dados local via TCP | `tslink add database --tcp localhost:5432` |
| Usar uma API HTTP de modelo local, como o Ollama | `tslink add model --proxy localhost:11434` |

Para o Ollama, obtenha a URL exata com `tslink url model --wait`; o `baseURL` de um cliente compatível com OpenAI usa essa URL acrescida de `/v1`. [Modelos locais e fluxos com dados privados →](local-ai.md)

Para vários apps em um host, o TSLink reúne nós com nome, listas de identidades permitidas para HTTP, expiração do Funnel e gerenciamento MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) pode bastar para um app nos seus próprios dispositivos.

<a id="architecture"></a>

## Arquitetura

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Exemplo de mapa de serviços: App, Docs, Database e Model são nós com nomes distintos na mesma tailnet. Apps, arquivos e APIs de modelos usam HTTPS; o banco de dados usa TCP privado." width="960">
</picture>

**Uma tailnet, nós de serviço distintos.** Um daemon compartilhado executa um nó tsnet incorporado por serviço, encaminhando HTTP, servindo arquivos ou atuando como proxy TCP. As alterações no registro entram em vigor durante a execução. Cada nó tem sua própria identidade de rede; os serviços compartilham a máquina que os publica.
[Detalhes da arquitetura →](architecture.md)

| Componente | Função |
|---|---|
| [Go](../go.mod) | Executável nativo de linha de comando |
| [Tailscale tsnet](architecture.md) | Nós de serviço e transporte da tailnet |
| [Cobra](https://github.com/spf13/cobra) | Comandos e ajuda |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transportes para agentes |
| Chaveiro do sistema e gerenciador de serviços do usuário | Credenciais opcionais e execução em segundo plano |

Os serviços permanecem na sua tailnet, a menos que você habilite explicitamente o [Funnel público](getting-started.md#more-examples). Os serviços HTTP e de arquivos aceitam listas de identidades autorizadas (`WhoIs`, `--allow`); TCP usa as políticas da tailnet e a autenticação do próprio backend. Consulte os [limites do compartilhamento](sharing.md).

O TSLink não instala apps, não executa modelos, não isola processos do host nem agrega vários hosts. Rede, criptografia e HTTPS vêm do Tailscale; o TSLink é um projeto independente.

<a id="agents"></a>

## Para agentes

As **19 ferramentas MCP** permitem que um agente compartilhe relatórios, gerencie serviços, obtenha URLs e inspecione a configuração. Conecte um cliente MCP local ao executável instalado:

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

O MCP gerencia o TSLink; as aplicações usam a API HTTP do modelo para inferência. Consulte [clientes MCP](mcp-clients.md), [MCP remoto](remote-mcp.md) e o [guia de operação para agentes](../AGENTS.md) para configuração e automação.

A automação CLI oferece `--json` com `schema_version` igual a `1`; consulte `tslink status --urls --json`. MCP local usa JSON-RPC por stdio. Veja [automação JSON](json-automation.md).

<a id="roadmap"></a>

## O que vem a seguir

Itens Em integração, Em revisão ou Planejado não estão incluídos na instalação a partir do código-fonte acima.

| Caso de uso | Status |
|---|---|
| <!-- roadmap:people --> Dê a um parente 3 dias de acesso a apps HTTP/arquivos privados e reúna os convites em uma mensagem; o destinatário ainda precisa do Tailscale. | Em integração |
| <!-- roadmap:health --> Verifique a saúde dos apps e receba alertas de queda ou expiração por um comando ou webhook opcional. | Em integração |
| <!-- roadmap:recipes --> Encontre apps compatíveis em loopback e visualize receitas de apps auto-hospedados antes de compartilhar. | Em integração |
| <!-- roadmap:limits --> Defina o tamanho de upload e os tempos limite de cada app HTTP para uploads grandes e clientes lentos. | Em integração |
| <!-- roadmap:windows --> Reinicie um daemon do Windows após falha enquanto o usuário permanecer com a sessão do Windows iniciada, com uma tarefa agendada e supervisor integrado. | Em integração |
| <!-- roadmap:access-log --> Veja quem abriu qual app nos logs locais de acesso, com os modos de caminho `prefix`, `full` ou `off`. | Em revisão |
| <!-- roadmap:portal --> Abra uma página inicial com os apps permitidos e o encaminhamento de registro para proprietários; visitantes ainda precisam do Tailscale. | Em revisão |
| <!-- roadmap:mcp-scopes --> Defina um papel e um escopo de apps para um agente, com registros de auditoria de suas alterações. | Em revisão |
| <!-- roadmap:guest-links --> Permita que um convidado abra um app HTTP no navegador sem instalar Tailscale, com link temporário e PIN opcional, por Funnel público com controle de acesso. | Em revisão |
| <!-- roadmap:durations --> Escolha durações predefinidas ou personalizadas de pelo menos 1 hora, com máximo de 7 dias para convidados por padrão, configurável. | Em revisão |
| <!-- roadmap:requests --> Ajude usuários de celulares a entrar por QR code; permita que o proprietário aprove solicitações de acesso a apps ou de tempo extra em uma ação. | Em revisão |
| <!-- roadmap:multi-host --> Veja apps de vários hosts em um inventário. | Planejado |

<a id="documentation"></a>

## Documentação e licença

[Primeiros passos](getting-started.md) · [Modelos locais](local-ai.md) ·
[Referência da CLI](cli-reference.md) · [Plataformas](platforms.md) · [Roadmap](roadmap.md)

Para contribuir, siga [CONTRIBUTING.md](../CONTRIBUTING.md); relate vulnerabilidades conforme [SECURITY.md](../SECURITY.md).

O TSLink usa a [licença Apache 2.0](../LICENSE) sem modificações, incluindo uso comercial. Preserve o [NOTICE](../NOTICE) aplicável e os [avisos de terceiros](../THIRD_PARTY_NOTICES.md) ao redistribuir. A [cooperação comercial](../COMMERCIAL.md) é voluntária e não acrescenta condições à licença. Os termos de serviço e planos do Tailscale se aplicam separadamente.
