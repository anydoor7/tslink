<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>내 Tailscale 네트워크 안에서, 앱마다 비공개 주소를.</strong></p>
<p align="center">내 기기에서 열어 보세요. 원하는 날짜까지 특정 사람에게 또는 링크로 공유하세요.</p>
<p align="center"><a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <strong>한국어</strong> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">다른 언어</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## 설치

```sh
brew install --cask anydoor7/tap/tslink
```

Linux용 `.deb`, `.rpm` 패키지와 Windows 빌드는 [최신 릴리스](https://github.com/anydoor7/tslink/releases/latest)에 있습니다. 앱을 처음 공유할 때 TSLink가 그 앱의 Tailscale 로그인 링크를 보여 줍니다. [시작하기](getting-started.md)

<a id="why"></a>

## TSLink가 필요한 경우

내 기기에서 앱 하나를 여는 정도라면 Serve로 충분합니다. TSLink는 앱 주소, 기한, 접근 변경을 하나의 흐름으로 다룹니다.

| 작업 | Tailscale만 사용 | TSLink |
|---|---|---|
| 휴대폰에서 웹 앱 하나 열기 | `tailscale serve 3000`이면 충분 | `tslink share 3000` |
| 앱 여러 개, 이름은 따로 | Services 설정 또는 별도 노드 | 앱마다 `share`/`add` 한 번, 노드마다 등록 |
| 한 사람에게 앱 하나를 7일간 | 정책 규칙을 쓴 뒤 JIT(임시 접근) 도구나 수동 삭제 | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/파일) |
| 브라우저 링크, 3일간 | 공개 Funnel. 접근 관문과 예약 종료는 직접 추가 | `tslink guest create photos --for 3d --public --print-link` (HTTP만) |

비공개로 공유받는 사람은 Tailscale이 필요합니다. 게스트 링크는 공개된 접근 자격 증명이며 다른 사람에게 전달할 수 있습니다.

[전체 비교](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## 내 앱을 내 기기에서

- **앱마다 주소 하나.** 웹 앱, 폴더, 단일 파일, TCP 포트가 tailnet 안에서 각자 이름을 가지므로 IP 주소 대신 이름을 씁니다.
- **기본은 비공개.** 게스트 링크를 만들거나 Funnel로 공개하기 전에는 아무것도 공개되지 않습니다.
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

<a id="agents"></a>

## AI 에이전트용

에이전트가 `localhost`에서 띄운 개발 서버는 휴대폰에서 닿지 않습니다. TSLink를 쓰면 에이전트가 그 서버에 비공개 주소를 주고, 정확한 URL을 알려 주고, 끝나면 지울 수 있습니다.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI 또는 MCP.** 관리 명령은 `--json`을 받아 버전이 붙은 결과를 돌려주고, `tslink mcp`는 앱과 접근 관련 작업을 MCP로 제공합니다.
- **제한된 역할.** `viewer`, `app-operator`, `people-manager` 중 하나를, 지정한 앱 범위로 줍니다. `tslink mcp-audit`로 에이전트가 바꾼 내용을 볼 수 있습니다. 역할은 TSLink의 도구를 제한할 뿐, 에이전트 자신의 셸까지 막지는 않습니다.

[에이전트 빠른 시작](agent-quickstart.md) · [MCP 권한](mcp-scopes.md) · [원격 MCP](remote-mcp.md)

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
