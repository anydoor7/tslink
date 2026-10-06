<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Dê a cada app do seu computador ou servidor um endereço privado próprio na sua rede Tailscale, e decida quem pode acessá-lo.</strong></p>

Abra seus apps web, pastas, APIs de modelos e bancos de dados no seu próprio celular e notebook, com verificação de saúde e histórico de acesso para cada um. Seus agentes de IA podem dar aos apps que iniciam em localhost um endereço privado para seus outros dispositivos e verificá-los, dentro do papel que você der a eles. Quando outra pessoa precisar entrar, conceda acesso a uma pessoa específica até uma data ou abra um app web para a internet por tempo limitado.

**Requer Tailscale.** Você precisa de uma conta Tailscale (gratuita para uso pessoal), e cada dispositivo que abre um app privado precisa do app Tailscale; convidados e visitantes públicos só precisam de um navegador. O TSLink é um projeto independente, não criado nem endossado pela Tailscale. [Requisitos](#requirements)

<p align="center"><a href="#quickstart">Início rápido</a> · <a href="#agents">Para agentes</a> · <a href="comparison.md">Comparação com Serve, ngrok e Cloudflare</a> · <a href="#documentation">Documentação</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <strong>Português (Brasil)</strong> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## O que você pode fazer

### Acesse seus próprios apps

- **Um endereço para cada app.** `tslink share 3000`, `tslink share ./photos` ou `tslink add db --tcp localhost:5432` dá a um app web, pasta, arquivo ou serviço TCP um endereço privado próprio no seu tailnet (sua rede Tailscale privada), como `https://photos.<tailnet>.ts.net`. Cada app é um dispositivo Tailscale separado, então você abre os apps pelo nome em vez de por endereço IP e porta.
- **Privado, a menos que você escolha outra coisa.** O TSLink mantém o acesso aos apps privado por padrão, e a política do seu tailnet decide quais dispositivos podem se conectar. Ele só abre um endpoint público de app quando você cria um link de convidado ou publica explicitamente pelo Funnel.
- **Tudo em um só lugar.** `tslink status --urls` lista os apps registrados neste computador, e uma página inicial privada opcional mostra os endereços e a saúde deles. [Portal](portal.md)
- **Saiba quando algo quebra.** Verificações de saúde em segundo plano podem avisar você por um comando ou webhook quando um app cai ou volta, ou quando o login do Tailscale dele está para expirar. O histórico de acesso mostra quem abriu qual app e quando, incluindo pedidos negados. [Saúde e alertas](health-and-alerts.md) · [Histórico de acesso](access-log.md)
- **Receitas de apps.** Há receitas para 15 apps auto-hospedados, incluindo Home Assistant, Jellyfin, Immich e Ollama, e `tslink apps detect` pode encontrar apps compatíveis que já estão escutando localmente. Para uploads grandes de fotos e vídeos, [aumente os limites de upload de cada app](sharing.md). [Receitas de apps](apps.md) · [IA local](local-ai.md)

### Deixe seus agentes trabalharem com eles

Quando um agente inicia um servidor de desenvolvimento, uma prévia ou uma API de modelo local em `localhost`, seu celular e seus outros computadores não conseguem alcançar esse endereço. O TSLink deixa o agente dar a ele um endereço privado, informar a URL exata e remover o registro depois, dentro dos limites que você definir.

- **Compartilhar, verificar, desfazer.** `share --json` retorna o nome registrado e também a URL exata ou um link de login para você abrir. `url <name> --wait` e `status --urls --name <name>` informam se o endpoint está pronto, e `remove <name>` (`unshare` no MCP) remove o compartilhamento. [Guia de agentes](agent-quickstart.md)
- **Feito para automação.** Os comandos de gerenciamento, exceto `tslink mcp`, aceitam `--json` e retornam resultados versionados com códigos de erro estáveis. `tslink mcp` expõe ferramentas de apps e de acesso a um cliente MCP local via JSON-RPC. Com os vínculos de chamadores configurados, `tslink serve --mcp` expõe essas ferramentas a clientes MCP nos seus outros dispositivos pelo tailnet. [Automação JSON](json-automation.md) · [MCP remoto](remote-mcp.md)
- **Autoridade limitada.** Um agente local tem autoridade de proprietário por padrão. Dê a um agente um papel reduzido (`viewer`, `app-operator` ou `people-manager`) que cobre só os apps que você indicar e limita por quanto tempo qualquer acesso concedido por ele pode durar. Mudanças feitas pelo MCP são registradas, e `tslink mcp-audit` as mostra. Papéis limitam as ferramentas do TSLink, não o shell nem os arquivos do próprio agente. [Permissões MCP](mcp-scopes.md)

### Compartilhe com quem você escolher

- **Pessoas específicas, até uma data.** `tslink people add alice@example.com --apps photos,notes --for 7d` deixa esse login do Tailscale abrir esses apps web e de arquivos até o prazo. `people update`, `extend` e `people remove` alteram ou encerram o acesso; depois da remoção, o próximo pedido da pessoa é negado, mas o que ela já baixou não volta. [Pessoas](people.md) · [Prazos](durations.md)
- **Alguém fora do seu tailnet.** Adicione `--invite --print-links` para receber uma mensagem pronta para enviar, com um convite de dispositivo para cada app (isso exige um token de API de um usuário). `--qr` mostra um código para configurar o celular.
- **Pedidos.** Pessoas no seu tailnet podem pedir mais tempo, ou acesso a um app que você marcou como solicitável, pela página inicial. Você aprova com um prazo em um único comando. [Pedidos de acesso](requests.md)

### Abra um app web para a internet, por um tempo

- **Links de convidado.** `tslink guest create photos --for 3d --public --print-link` cria um link de navegador para um app web, com PIN opcional, que você pode revogar separadamente. Convidados não precisam de conta Tailscale. Qualquer pessoa com o link pode usá-lo, então ele não comprova quem visitou. [Convidados](guest-links.md)
- **Uma URL pública aberta.** `tslink add preview --proxy localhost:3000 --funnel --public` publica um app web para qualquer pessoa que tenha a URL. Uma nova publicação dura 24 horas por padrão; use `--funnel-ttl` para escolher outra duração. [Funnel](funnel.md)
- Novos links de convidado e novas publicações públicas abertas usam o Tailscale Funnel e têm duração limitada (mínimo de 1 hora, máximo padrão de 7 dias, configurável pelo proprietário). Esses caminhos públicos aceitam apps com proxy HTTP; serviços diretos de pastas ou arquivos e TCP puro continuam privados.

Tudo isso faz parte da v0.1.0.

<a id="requirements"></a>

## Requisitos

O TSLink é construído sobre o Tailscale. É um projeto independente, não criado nem endossado pela Tailscale, e os termos e [planos](https://tailscale.com/pricing) da própria Tailscale se aplicam.

| Quem | O que precisa |
|---|---|
| Você | Uma conta Tailscale com [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) ativados. O plano Personal gratuito é para uso não comercial. |
| O computador ou servidor que roda seus apps | Só o TSLink. Ele já embute o Tailscale, então não é preciso instalar o Tailscale à parte. Na configuração padrão, cada novo nó de app precisa de login no navegador e pode precisar de aprovação do dispositivo. [Credenciais armazenadas](credentials-and-tags.md) permitem o registro sem login no navegador para cada app. |
| Seus outros dispositivos | O app Tailscale, conectado ao seu tailnet. |
| Pessoas que você escolher | O app Tailscale e o próprio login. Elas entram no seu tailnet, o que adiciona um usuário ao seu plano, ou aceitam um convite de dispositivo para cada app. A política do seu tailnet precisa permitir o acesso delas. |
| Convidados e visitantes públicos | Um navegador. Seu tailnet precisa permitir o Funnel, que a Tailscale ainda considera beta. |

Quando um certificado HTTPS é emitido para um app, o nome de dispositivo dele no Tailscale e o nome DNS do seu tailnet aparecem em um log público de certificados. Escolha nomes de app que você não se importe que outros vejam.

<a id="installation"></a>
<a id="quickstart"></a>

## Início rápido

No macOS ou no Linux, instale com o Homebrew. O binário para macOS é assinado com um certificado Developer ID e notarizado pela Apple. Para atualizar depois, execute `brew upgrade --cask tslink` e, em seguida, `tslink install` de novo se o TSLink rodar como serviço em segundo plano.

```bash
brew install --cask anydoor7/tap/tslink
```

No Windows, baixe `tslink_<version>_windows_<arch>.zip` da [versão mais recente](https://github.com/anydoor7/tslink/releases/latest), confira-o com o `checksums.txt` e execute `tslink install` para que o TSLink inicie quando você entrar na sessão. O zip não tem assinatura Authenticode; [verifique a versão](verify-release.md) pelos checksums assinados e pelas atestações. Os pacotes `.deb` e `.rpm` para Linux estão na mesma página da versão. Para compilar a partir do código-fonte, você precisa de **Git e Go 1.26.6+**. Os comandos abaixo usam bash/zsh; veja [Configuração de macOS, Linux e Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Você precisa de uma **conta Tailscale** e [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Dispositivos de acesso privado precisam de Tailscale e permissão na política de rede. O TSLink incorpora Tailscale no host dos apps.

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
