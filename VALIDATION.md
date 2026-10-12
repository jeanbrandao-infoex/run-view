# Validação da base — 11/10/2026

## Executado e aprovado

- Toolchain Go 1.26.0 local temporário, arquivo conferido por SHA256 oficial: `aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235`.
- `go test -race ./...`: aprovado. Teste real runsc é opt-in e ficou skipped.
- `go vet -buildvcs=false ./...`: aprovado.
- `go build -buildvcs=false ./...`: aprovado.
- Compilação de sintaxe Python do harness e exemplo: aprovada.
- Smoke HTTP real de API compilada: criação, retry idempotente, tenant diferente recusado, cancelamento de pendente e purge.
- Fixture do exemplo XLSX: ordem de contas, valores, delta, percentual e base zero conferidos com openpyxl. Executado código confiável do exemplo com paths temporários; não executado Python de usuário fora de sandbox.
- Sintaxe YAML e separação de mounts/rede API/worker conferidas por parser. Isso não substitui `docker compose config` nem execução real.

O ambiente inicial não tinha Go. O compilador foi obtido apenas para este workspace; não foi instalado na máquina do usuário. `-buildvcs=false` foi necessário porque este workspace não tinha checkout Git válido durante a primeira compilação.

## Não executado

- Build de imagens Docker e resolução/instalação das dependências da imagem sandbox.
- `docker compose up` e validação do runtime gVisor.
- Isolamento real, limites cgroup, cancelamento/destruição de container e coleta tmpfs via Docker.
- OOM, fork bomb, falta de disco e testes de carga.
- Deploy na VPS/Dokploy.
- Integração ao Hubview, providers de IA ou R2.
- Extração/reconciliação de DRE em PDF real.

## Gate de produção

Homologar em servidor Linux com Docker/runsc antes de disponibilizar a ferramenta ao consultor. O teste `TestRunscIntegration` ajuda no smoke; os demais cenários da spec também são necessários. Não interpretar os testes com fake runner como verificação de segurança do sandbox.

## Diferenças e limites do MVP

- Persistência em spool local com flock, sem PostgreSQL/Redis. Uma API e um worker por instalação; escala posterior exige evolução da fila.
- Entrada base64 limitada, sem fetch de URLs ou R2 no executor. Evita SSRF/segredos de storage; o cliente do Hub fará o transporte.
- Sandbox novo por job, com checkpoints explícitos entre etapas. Não mantém um contêiner permanente por conversa.
- Worker controla o Docker por socket. O socket confere poder amplo no host; só API/sandbox ficam sem ele. Broker mais estreito é evolução de hardening.
- Limites de recursos estão fixados nesta base e timeout admite redução por requisição. Parametrização pela TI pode ser adicionada sem permitir aumento arbitrário pelo modelo.
- Preços/tokens, consentimento Continuar e quotas pertencem ao Hub e não foram implementados aqui.
- Parser universal de PDFs, OCR e templates financeiros validados permanecem fases próprias.
