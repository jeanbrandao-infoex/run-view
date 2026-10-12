# Infoex Run

Executor independente para análises do Assistant View. API Go, worker Go e sandbox Python obrigatório via Docker + gVisor. Código inicial entregue; homologação Docker/runsc e integração com Hub ainda pendentes.

Leia [docs/spec.md](docs/spec.md), [VALIDATION.md](VALIDATION.md) e [docs/hub-integration.md](docs/hub-integration.md).

## Desenvolvimento local

Go 1.26.0+, Linux (incluindo WSL2). A persistência usa flock e não suporta execução nativa do serviço em Windows ou NFS.

```bash
go test -race ./...
go vet ./...
go build ./...
```

API isoladamente, sem executar análises:

```bash
export INFOEX_RUN_TOKEN="$(openssl rand -hex 32)"
export INFOEX_RUN_DATA="$PWD/data"
go run ./cmd/api
```

O segredo é local ao terminal e não deve ser copiado para prints ou commits. Não confundir liveness da API com prontidão do worker.

## VPS / Dokploy

Requer Docker Compose, gVisor registrado como runtime `runsc` e recursos livres suficientes. Sem acesso à sua VPS, nada foi instalado/publicado por esta entrega.

1. Clone/copie o projeto no servidor. Preserve configuração de Docker já existente.
2. Instale e configure gVisor conforme documentação oficial. O restart do Docker necessário à configuração pode interromper todos os serviços da VPS: agende antes.
3. Faça `docker run --runtime=runsc --rm hello-world` para conferir o runtime.
4. Prepare um spool dedicado, não dentro do volume do banco:

```bash
sudo mkdir -p /opt/infoex-run/data
sudo chown 10001:10001 /opt/infoex-run/data
sudo chmod 700 /opt/infoex-run/data
docker build -t infoex-run-sandbox:local sandbox
docker image inspect infoex-run-sandbox:local --format '{{.Id}}'
stat -c '%g' /var/run/docker.sock
```

5. Crie uma vez a rede privada de controle no host: `docker network create --internal infoex-control`.
6. Copie `.env.example` para `.env`. Preencha token aleatório, digest/ID mostrado da imagem, GID do socket, caminho absoluto do spool e `INFOEX_RUN_CONTROL_NETWORK=infoex-control`. O mesmo caminho precisa existir no host Docker e no worker.
7. Importe `compose.yaml` em um projeto Compose dedicado no Dokploy ou rode:

```bash
docker compose config --quiet
docker compose up --build -d
docker compose logs --tail=40 api worker
```

Não configure domínio público/Traefik para o serviço. O Compose expõe apenas `127.0.0.1:8090`. Para Hub em outro projeto Compose, conecte apenas a API e o backend do Hub à rede externa e interna configurada em `INFOEX_RUN_CONTROL_NETWORK`; ele a acessa por `http://infoex-run-api:8090`, pois `127.0.0.1` dentro do Hub não é o host. Não conectar sandbox ou PostgreSQL a essa rede.

O worker possui Docker socket e, portanto, autoridade ampla sobre o daemon. É uma escolha explícita deste MVP; API e código da IA não recebem o socket. Não colocar projetos não confiáveis nesse worker. Para produção, homologar e considerar broker/daemon dedicado ou VPS separada.

Limites do worker Compose não limitam por si só os containers que ele cria; o runner define CPU/memória/PIDs/tmpfs de cada execução separadamente. Uma execução ativa por vez.

## Postman / primeira chamada

Variáveis privadas: `RUN_URL`, `RUN_TOKEN`, `TENANT_ID`. Exemplo de URL local: `http://127.0.0.1:8090`.

```http
POST {{RUN_URL}}/v1/jobs
Authorization: Bearer {{RUN_TOKEN}}
X-Tenant-ID: {{TENANT_ID}}
Idempotency-Key: analysis-001
Content-Type: application/json
```

```json
{
  "code": "from pathlib import Path\nPath('/workspace/result.csv').write_text('account,value\\na,100\\n')\nprint('1 conta processada')",
  "inputs": [],
  "timeout_seconds": 30
}
```

Guarde o `id`. Com os mesmos headers de autenticação/tenant:

- `GET /v1/jobs/<id>`: acompanhar resultado.
- `GET /v1/jobs/<id>/outputs`: listar artefatos.
- `GET /v1/jobs/<id>/outputs/result.csv`: baixar.
- `POST /v1/jobs/<id>/cancel`: cancelar.
- `DELETE /v1/jobs/<id>`: remover registro/bytes após conclusão e cópia segura ao R2.

GET não retorna código nem entradas; o resumo pode conter dados da análise, então só é retornado ao serviço autorizado. Não colocar token no frontend.

`examples/compare_dre.py` mostra comparação de dois CSVs normalizados e gera XLSX. Ele não extrai uma DRE arbitrária de PDF e não soma contas/subtotais automaticamente.

## Homologação real obrigatória

Com Go no mesmo host Docker, imagem construída e digest exportado:

```bash
export INFOEX_RUN_IMAGE='sha256:COLE_O_ID_REAL_DA_IMAGEM'
INFOEX_RUN_INTEGRATION=1 go test -v ./internal/runner -run TestRunscIntegration
```

O teste verifica execução real, arquivo gerado, entradas read-only, rede externa indisponível, socket ausente e timeout. Precisa usar caminhos visíveis ao daemon; não rodar contra Docker remoto com spool local.

Antes de produção, executar também os cenários de homologação de `docs/spec.md`: OOM, excesso de processos, saída gigante, reinício do worker, cancelamento e falta de espaço. Um smoke test não certifica toda a segurança do ambiente.

## Operação

- Uma API e um worker por spool. Não escalar réplicas sem evoluir persistência/leases.
- Jobs iniciados interrompidos viram `failed`; o Hub decide novo job, sem retry automático oculto.
- Container termina/removido depois da coleta; não existe sessão Python permanente.
- Falha de limpeza bloqueia purge. Corrigir Docker/runtime e reiniciar worker; startup limpa containers do serviço e reconcilia estados.
- Retenção máxima de 100 registros inclui jobs finalizados. Fazer purge autenticado após envio/verificação no R2. Reservar disco e monitorar; não compartilhar volume com PostgreSQL.
- Idempotência deixa de existir depois de DELETE. O Hub preserva sua chave/estado de geração.
- Arquivos no spool contêm dados dos clientes. Backup/cifra/retensão precisam da política da Infoex. Não enviar spool a este repositório.
- Rede privada e TLS/VPN se mover para outro host; o token não cifra HTTP.
- Fixar digests de imagens do serviço e dependências transitivas após build validado; tags dos Dockerfiles são base de desenvolvimento, não uma declaração de supply-chain auditada.

## Limitações atuais

Sem R2 direto, sem integração com Hub, sem SSE próprio, sem OCR, sem parser contábil universal, sem continuidade automática, sem quotas mensais e sem multiworker. São responsabilidades/fases descritas na spec. Não houve execução de código não confiável no host durante o desenvolvimento.
