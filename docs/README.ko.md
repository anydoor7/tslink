<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink는 컴퓨터의 앱마다 <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> 형태의 비공개 주소를 만들어 주는 오픈 소스 CLI 겸 MCP 서버입니다. 직접 하든 coding agent에게 맡기든, 실행 중인 개발 서버를 휴대폰에서 열 수 있습니다. 독립 프로젝트이며 Tailscale에서 만든 것이 아닙니다.</p>
<p align="center">휴대폰에는 Tailscale 앱이 필요하고, 같은 tailnet에 로그인해 있어야 합니다.</p>
<p align="center"><a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <strong>한국어</strong> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">다른 언어</a></p>

```text
# Claude Code: 먼저 CLI를 설치하세요(아래 설치 참고)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# mcpServers JSON을 쓰는 로컬 MCP 클라이언트(다른 클라이언트는 해당 클라이언트의 MCP 설정 참고)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

에이전트에게 말하기: "개발 서버를 공유해서 휴대폰에서 열 수 있게 해 줘."
```

같은 일을 직접 하는 방법입니다. 개발 서버가 이미 5173 포트에서 대기 중이라고 가정합니다. `--json`을 붙이면 `share`가 등록된 이름을 `data.name`으로 알려 줍니다. 이후 명령에는 이 이름을 쓰세요. `--name`으로 준 이름이 이미 사용 중이면 `web-2`처럼 달라질 수 있습니다.

```sh
tslink share 5173 --name web --json   # data.name이 이름. data.url이 정확한 URL이고, status가 needs_login이면 data.auth_url이 Tailscale 로그인 링크
tslink url <name> --wait              # 로그인을 마친 뒤 정확한 URL 확인
tslink remove <name>                  # 다 쓰면 삭제. share가 실패했다면 건너뜀. 개발 서버는 계속 실행됨
```

<a id="installation"></a>
<a id="quickstart"></a>

## 설치

macOS 호스트에는 macOS 13 Ventura 이상이 필요합니다([플랫폼 지원](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Windows에서는 Scoop으로 설치합니다:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

업그레이드하려면 `brew upgrade --cask tslink` 또는 `scoop update; scoop update tslink`를 실행하세요. TSLink를 백그라운드 서비스로 실행한다면 업그레이드 후 `tslink install`을 다시 실행하세요.

Linux용 `.deb`, `.rpm` 패키지와 Windows 빌드는 [최신 릴리스](https://github.com/anydoor7/tslink/releases/latest)에 있습니다. 앱을 처음 공유할 때 TSLink가 그 앱의 Tailscale 로그인 링크를 보여 줍니다. [시작하기](getting-started.md)

<a id="agents"></a>

## AI 에이전트용

에이전트가 `localhost`에서 띄운 개발 서버는 휴대폰에서 닿지 않습니다. TSLink를 쓰면 에이전트가 그 서버에 비공개 주소를 주고, 정확한 URL을 건네고, 끝나면 공유를 지울 수 있습니다. 휴대폰에는 Tailscale 앱이 필요하고, 같은 tailnet에 로그인해 있어야 합니다.

2026-10-22에 Vite 8.3.5와 iPhone 15 Pro로 테스트했습니다. 에이전트가 MCP로 개발 서버를 공유했고, 휴대폰에서 비공개 URL을 열었으며, 저장하면 새로 고침 없이 페이지가 바뀌었습니다.

Claude Code에서는 먼저 CLI를 설치한 다음 아래를 실행하세요.

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

플러그인에는 [미리보기 스킬](../skills/tslink-preview/SKILL.md)과 아래의 MCP 연결이 들어 있습니다. 마켓플레이스는 이 저장소에 있고, 플러그인은 [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin)에서 가져옵니다. 기준이 되는 스킬은 여기에 있고, 플러그인에는 CI로 검증하는 사본이 들어 있습니다. 다른 에이전트도 같은 스킬을 쓰거나 MCP에 직접 연결할 수 있습니다. 이 연동은 로컬 CLI가 필요하며 claude.ai 채팅에서는 동작하지 않습니다. [설정 및 등록](agent-integrations.md)

`mcpServers` JSON을 쓰는 로컬 MCP 클라이언트라면(다른 클라이언트는 해당 클라이언트의 MCP 설정 안내와 [MCP 클라이언트 가이드](mcp-clients.md) 참고):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **개발 서버마다 이름 하나.** 공유할 때마다 루트 경로, 443 포트의 전용 비공개 주소 `https://<name>.<tailnet>.ts.net`이 생깁니다. 이름이 이미 쓰이고 있으면 숫자 접미사가 붙으므로, 에이전트가 돌려받은 URL을 사용하세요.
- **새 앱마다 사람이 필요합니다.** 기본 설정에서는 새 앱 노드마다 사람이 Tailscale 등록을 한 번 마쳐야 합니다. 결과가 `needs_login`이면 로그인을 마친 뒤 `tslink url <name> --wait`로 정확한 URL을 받으세요. 별개로, 새 이름에 접속할 수 있게 되기까지 시간이 걸릴 수 있습니다. 두 번의 테스트에서는 36초에서 38초였습니다.
- **누가 열 수 있나.** 기본적으로는 정책이 허용하는 tailnet 기기입니다. MCP `share`는 `allow`를 받아 내 로그인으로 제한할 수 있고, CLI에서는 `tslink add web --proxy localhost:5173 --allow you@example.com`을 사용합니다. 이름은 공개 인증서 로그에 남으므로 비밀이 아닙니다.
- **수정하고 저장한 뒤 결과를 확인하세요.** 코드를 저장하면 휴대폰의 페이지가 갱신됩니다. 앱 자체의 Host 및 Origin 규칙이 요청을 막을 수는 있습니다. 테스트에서 Vite는 `allowedHosts`를 바꿀 필요가 없었고, Next.js는 테스트하지 않았으며 여전히 `allowedDevOrigins`가 필요합니다.
- **정리.** 에이전트가 `unshare`를 호출하거나 직접 `tslink remove <name>`을 실행하며, 항상 share 결과가 돌려준 이름을 씁니다. 삭제하면 공유 등록이 해제되고, TSLink가 자기 소유임을 확인할 수 있을 때는 tailnet 노드도 삭제를 시도합니다. 결과의 정리 또는 남은 상태 필드를 확인하세요. 개발 서버는 멈추지 않습니다.
- **CLI 또는 MCP.** 관리 명령은 `--json`을 받아 버전이 붙은 결과를 돌려주고, `tslink mcp`는 앱과 접근 관련 작업을 MCP로 제공합니다.
- **폴더가 아니라 파일 하나.** 에이전트는 `tslink share ./report.html`로 그 자체로 완결된 HTML 보고서만 공유할 수 있습니다. 같은 폴더의 다른 파일에는 닿지 않습니다.
- **제한된 역할.** `viewer`, `app-operator`, `people-manager` 중에서 고르며, 지정한 앱으로 범위가 좁혀집니다. 새 앱을 공유하려면 `owner` 역할이 필요합니다. `tslink mcp-audit`으로 에이전트가 바꾼 내용을 확인할 수 있습니다. 역할이 제한하는 것은 TSLink의 도구이며, 에이전트 자신의 셸은 제한하지 않습니다.

[에이전트 빠른 시작](agent-quickstart.md) · [MCP 권한](mcp-scopes.md) · [원격 MCP](remote-mcp.md)

<a id="why"></a>

## TSLink가 필요한 경우

직접 띄운 앱 하나를 내 휴대폰에서 여는 정도라면 Serve로 충분합니다. TSLink는 에이전트가 띄우는, 대개 여러 개인 개발 서버를 위한 것입니다. 서버마다 전용 비공개 이름을 주고, 공유·URL 확인·정리를 CLI, MCP, Claude Code 스킬로 바로 처리할 수 있습니다.

| 작업 | Tailscale만 사용 | TSLink |
|---|---|---|
| 에이전트가 띄운 개발 서버 여러 개, 각각 이름 하나 | Serve는 기기 한 대에 이름 하나를 두고 기기 단위 경로를 설정, Services는 이름마다 서비스 정의, 승인, 태그가 붙은 호스트가 필요 | 앱마다 노드 하나. 공유, URL 확인, 정리를 CLI(`share`, `url`, `remove`), MCP(`share`, `url`, `unshare`), 스킬에 묶어 제공 |
| 직접 띄운 앱 하나를 휴대폰에서 열기 | `tailscale serve 3000`이면 충분 | `tslink share 3000` |
| 한 사람에게 앱 하나를 7일간 | 정책 규칙을 쓴 뒤 JIT(임시 접근) 도구나 수동 삭제 | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/파일) |
| 브라우저 링크, 3일간 | 공개 Funnel. 접근 관문과 예약 종료는 직접 추가 | `tslink guest create photos --for 3d --public --print-link` (HTTP만) |

비공개로 공유받는 사람은 Tailscale이 필요합니다. 게스트 링크는 공개된 접근 자격 증명이며 다른 사람에게 전달할 수 있습니다.

[전체 비교](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## 내 앱을 내 기기에서

- **앱마다 주소 하나.** 웹 앱, 폴더, 단일 파일, TCP 포트가 tailnet 안에서 각자 이름을 가지므로 IP 주소 대신 이름을 씁니다.
- **기본은 비공개.** 게스트 링크를 만들거나 Funnel로 공개하기 전에는 아무것도 공개되지 않습니다.
- **컴퓨터 전체가 아니라 앱 하나.** 공개하는 앱마다 전용 노드가 생기고, 그 노드는 해당 앱으로만 전달합니다. 호스트에 Tailscale 앱이 없으면 TSLink는 호스트의 다른 포트를 tailnet에 추가하지 않습니다.
- **홈 페이지**에서 앱과 상태를 한눈에 봅니다. [포털](portal.md)
- **상태 점검과 알림**을 명령이나 webhook으로 받고, 접속 이력에는 거부된 요청도 남습니다. [상태 점검과 알림](health-and-alerts.md) · [접속 이력](access-log.md)
- **셀프 호스팅 앱 15개용 레시피**. Home Assistant, Jellyfin, Immich, Ollama 등이 있습니다. `tslink apps detect`는 이미 실행 중인 앱을 찾습니다. [앱 구성 레시피](apps.md)

## 원할 때 공유하기

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

게스트 링크와 새 공개 URL은 만료되며 웹 앱에만 쓸 수 있습니다. 폴더, 파일, TCP 포트는 비공개로 남습니다. [사용자 공유](people.md) · [게스트 링크](guest-links.md) · [공개 접속](funnel.md)

<a id="architecture"></a>

## 동작 방식

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="PC 또는 클라우드 호스트 하나에서 CLI/MCP가 공용 데몬과 앱별 노드를 관리합니다. 비공개 기기는 Tailscale 암호화 경로로, 선택적 공개 HTTPS/Funnel은 게스트 인증 또는 명시적 공개 설정을 통해 HTTP 앱에 연결됩니다." width="960">
</picture>

백그라운드 프로세스 하나가 앱마다 별도의 Tailscale 노드를 실행합니다. tailnet 전송과 HTTPS 인증서는 Tailscale이 제공합니다. 비공개 웹·파일 접속은 `--allow`와 사용자 공유로 Tailscale ID 기준 제한할 수 있고, 원시 TCP는 tailnet 정책과 앱 자체 로그인에 맡깁니다. [구조](architecture.md)

<a id="requirements"></a>

## 요구 사항

| 누구 | 필요한 것 |
|---|---|
| 나 | MagicDNS와 HTTPS를 켠 Tailscale 계정 |
| 앱을 실행하는 컴퓨터 | TSLink (Tailscale 내장, Linux에서는 systemd 사용자 세션 필요) |
| 내 기기와 공유 상대 | Tailscale 앱 |
| 게스트 | 브라우저 |

HTTPS 앱의 이름은 공개 인증서 로그에 나타나므로 남이 봐도 괜찮은 이름을 고르세요.

<a id="documentation"></a>

## 더 보기

[전체 문서](INDEX.md) · [CLI 참조](cli-reference.md) · [Serve, ngrok, Cloudflare와 비교](comparison.md) · [기여](../CONTRIBUTING.md) · [보안](../SECURITY.md)

Apache 2.0. TSLink는 독립 프로젝트이며 Tailscale이 만들거나 보증하지 않았습니다.
