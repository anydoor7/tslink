<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Acesse e gerencie seus apps de qualquer lugar.<br>Mantenha-os privados ou compartilhe do seu jeito.</strong></p>

Seus apps, no seu computador ou servidor na nuvem: acesse por uma rede privada criptografada ou escolha links de convidado no navegador ou acesso público. Opere você mesmo ou por um agente.

<p align="center"><a href="#quickstart">Início rápido</a> · <a href="#agents">Para agentes</a> · <a href="#documentation">Documentação</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <strong>Português (Brasil)</strong> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Seus apps sempre ao alcance

| O que você precisa | O que o TSLink oferece |
|---|---|
| Usar seus apps em vários dispositivos | Endereços privados para painéis domésticos, páginas web só locais, arquivos, APIs de modelos e serviços TCP em PC ou servidor. |
| Compartilhar com pessoas específicas | Apps HTTP/arquivos selecionados, identidade Tailscale verificada, expiração e revogação. Os destinatários usam Tailscale. [Pessoas](people.md) |
| Receber visitas pelo navegador | Links temporários com PIN opcional para apps proxy HTTP, ou HTTPS explicitamente público via Funnel. Links podem ser encaminhados e não comprovam identidade. [Convidados](guest-links.md) |
| Cuidar de vários apps | Inventário por host, portal privado, verificações de saúde e alertas, histórico de acesso e gestão CLI/MCP com papéis de agente, escopos por app e registros de auditoria. [Portal](portal.md) · [Permissões MCP](mcp-scopes.md) |

[Receitas de apps](apps.md), [limites de upload](sharing.md), [prazos flexíveis](durations.md) e [orientação por QR e pedidos de acesso](requests.md) ajudam na manutenção. Esses recursos estão neste código-fonte.

<a id="installation"></a>
<a id="quickstart"></a>

## Início rápido

Instale pelo código-fonte com **Git e Go 1.26.6+**; versões pré-compiladas e Homebrew ainda não foram publicados. Os comandos usam bash/zsh. [Configuração de macOS, Linux e Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Você precisa de acesso ao repositório, uma **conta Tailscale** e [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Dispositivos de acesso privado precisam de Tailscale e permissão na política de rede. O TSLink incorpora Tailscale no host dos apps.

Com seu app já rodando na porta 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Escolha um nome livre; se `share` retornar outro, use esse em `url`. Conclua primeiro o cadastro pelo navegador e a aprovação do dispositivo solicitados; depois abra a URL exata em um dispositivo autorizado. `share` inicia o serviço em segundo plano quando necessário. Esse primeiro uso privado não exige token API de administrador. Compartilhe arquivos com `tslink share ./report.html`; os arquivos devem existir e os apps estar rodando. [Configuração completa](getting-started.md)

Quando estiver funcionando e for útil, considere [dar uma estrela ao TSLink](https://github.com/anydoor7/tslink) para ajudar outras pessoas a descobri-lo. É totalmente opcional.

<a id="architecture"></a>

## Como tudo se conecta

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Um PC ou servidor na nuvem: CLI/MCP gerencia um daemon comum e nós por app. Dispositivos privados usam Tailscale criptografado; HTTPS/Funnel público opcional chega aos apps HTTP por controle de convidados ou publicação aberta explícita." width="960">
</picture>

Pense em um caminho privado e criptografado até seus apps. **O Tailscale fornece transporte de rede e HTTPS; o TSLink gerencia o acesso em cada host.** Um daemon executa um nó integrado por serviço. O portal privado mostra os apps permitidos; saúde e histórico ajudam na manutenção.

O acesso público precisa ser ativado: convidados precisam do link e do PIN opcional; Funnel aberto é acessível a qualquer pessoa com a URL. Ambos usam HTTPS público, não identidade privada de usuário. TCP puro continua privado, sujeito à política do tailnet e à autenticação do backend. O TSLink não instala apps, isola processos, cria VPC na nuvem nem agrega vários hosts. Projeto independente que funciona com Tailscale. [Arquitetura e limites](architecture.md)

<a id="agents"></a>

## Para agentes

Gerencie inventário, saúde, URLs e acesso por CLI/MCP. Comece pelo [guia de agentes](agent-quickstart.md), leia os esquemas atuais das ferramentas e confirme o acesso real antes de informar sucesso.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

A automação CLI usa `--json`; MCP usa JSON-RPC por stdio. [Clientes](mcp-clients.md) · [MCP remoto](remote-mcp.md) · [Papéis e escopos](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Documentação e licença

[Todos os guias](INDEX.md) · [Referência CLI](cli-reference.md) · [IA local](local-ai.md) · [Saúde](health-and-alerts.md) · [Histórico de acesso](access-log.md) · [Roteiro](roadmap.md)

O inventário multihost está planejado. [Contribuições](../CONTRIBUTING.md) e [relatos de segurança](../SECURITY.md) são bem-vindos. [Apache 2.0](../LICENSE) permite uso comercial; preserve [NOTICE](../NOTICE) e [avisos de terceiros](../THIRD_PARTY_NOTICES.md) ao redistribuir. A [cooperação comercial](../COMMERCIAL.md) é voluntária. Termos e planos do Tailscale se aplicam separadamente.
