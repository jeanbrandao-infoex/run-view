# Infoex Run — serviço independente de execução de análises

Versão 1.0 · 11/10/2026 · Implementação inicial incluída neste projeto.

## 1. Resultado esperado

Dar ao Assistant View capacidade de executar análises Python sobre arquivos autorizados, processar muitas linhas sem serializá-las todas como argumentos do modelo e gerar XLSX, CSV, HTML e gráficos.

Infoex Run não é provedor de IA, não autentica pessoas e não acessa os bancos dos clientes. O Hub continua responsável por usuário, empresa, entidade, conversa, quotas, chamadas ao modelo e armazenamento no R2.

Nome escolhido: **Infoex Run**. Identificador técnico: `infoex-run`. Não houve verificação de marca comercial ou domínio; não é requisito deste projeto interno.

## 2. Decisões de arquitetura

- Projeto independente do Hub e sem dependência do Softnery Kit: autenticação de serviço, não sessão humana.
- API e worker Go, imagem de análise Python com bibliotecas fixadas, Docker com runtime obrigatório `runsc`/gVisor.
- Implantação inicial autorizada como desenho na mesma VPS, em containers separados. A publicação real ainda depende de acesso/ação no servidor.
- Na mesma VPS, kernel, disco, Docker e recursos do host continuam compartilhados. Containers/gVisor não equivalem a VPS dedicada.
- API sem Docker socket. Worker é controlador privilegiado e recebe socket apenas para criar sandboxes com parâmetros fixos.
- Sandbox nunca recebe socket, segredos, rede, mounts arbitrários ou acesso ao PostgreSQL.
- Uma execução simultânea, uma API e um worker por spool no MVP.
- Job é unidade de execução. Cada novo job usa sandbox novo; continuidade entre etapas usa arquivos/checkpoints explícitos, não depende de container vivo.

## 3. Fronteiras de confiança

O Hub autoriza o acesso e envia somente entradas daquela operação. Infoex Run recebe token S2S e tenant em header; o tenant enviado pelo Hub é uma asserção confiável de serviço, não identidade de usuário verificada localmente.

Um token S2S comprometido permite criar jobs para tenants arbitrários. Não enviar esse token ao frontend. Serviço privado, TLS/mTLS ou túnel quando fora do mesmo host e rotação de segredo são requisitos operacionais.

O worker com Docker socket pode administrar o host Docker. Mesmo não expondo o socket à API, o worker segue sendo componente sensível. Rodar como usuário não root e restringir a API reduz exposição, mas o socket não é uma credencial de privilégio mínimo. Evolução de produção pode substituir o socket por broker local estreito ou daemon dedicado.

O código e os arquivos são não confiáveis. Resumos e artefatos gerados também não são prova de correção nem sinal de autorização. Validação de negócio e decisão de continuar permanecem no Hub.

## 4. Contrato implementado

Todos os endpoints de job exigem:

```http
Authorization: Bearer <segredo S2S com pelo menos 32 caracteres>
X-Tenant-ID: <ID estável da empresa>
```

### Criar execução

```http
POST /v1/jobs
Idempotency-Key: <ID da geração/etapa>
Content-Type: application/json
```

```json
{
  "code": "from pathlib import Path\nPath('/workspace/result.csv').write_text('account,value\\na,100\\n')\nprint('1 conta processada')",
  "inputs": [
    {"name": "dre.csv", "data_base64": "YWNjb3VudCx2YWx1ZQphLDEwMAo="}
  ],
  "timeout_seconds": 120
}
```

202 para criação; 200 para repetição idêntica; 409 se a chave já representar outro conteúdo; 429 quando fila/upload está cheio. Não aceitar campos extras, URLs, paths, imagens, comandos de Docker ou permissões do cliente.

| Endpoint | Resultado |
| --- | --- |
| `GET /healthz` | Liveness da API, não atesta worker/runsc |
| `GET /v1/jobs/{id}` | Estado, timestamps, resumo limitado, erro genérico e manifesto |
| `POST /v1/jobs/{id}/cancel` | Cancela pendente ou sinaliza cancelamento de execução |
| `GET /v1/jobs/{id}/outputs` | Manifesto de arquivos de job concluído |
| `GET /v1/jobs/{id}/outputs/{name}` | Download binário autenticado, sempre como anexo |
| `DELETE /v1/jobs/{id}` | Purge explícito de job terminal após limpeza do sandbox |

Estados: queued, running, completed, failed, cancelled. Um job iniciado que perde worker torna-se failed/worker_interrupted após limpeza na próxima inicialização; não há reexecução automática de tentativa ambígua.

Idempotência é mantida enquanto o registro existir. Após DELETE a mesma chave pode criar nova execução: o Hub deve manter sua própria deduplicação de gerações/cobranças e nunca depender exclusivamente do spool.

## 5. Persistência e recursos

Spool no disco local Linux, com JSON gravado atomicamente e flock entre processos. NFS, múltiplas réplicas e vários workers não suportados no MVP. O worker mantém lock de processo; duas instâncias não operam o mesmo spool.

Limites iniciais, escolhidos como defaults de implementação:

| Recurso | Limite |
| --- | --- |
| Timeout | 120 s padrão, até 300 s |
| CPU sandbox | 1 CPU |
| Memória sandbox | 512 MiB, sem swap adicional |
| Processos | 64 |
| Inputs | Até 20 arquivos, 24 MiB decodificados no total |
| Código | Até 64 KiB |
| Outputs | Até 20 arquivos regulares, 128 MiB no total |
| Diretório de trabalho | tmpfs de 128 MiB |
| Temporários | tmpfs de 32 MiB |
| Resumo | Até 8 KiB antes de codificação JSON |
| Jobs retidos | Até 100, incluindo concluídos |
| Uploads simultâneos na API | 1 |

O cap de jobs limita ocupação, mas o spool pode se aproximar de dezenas de GiB conforme outputs e entradas. Reservar disco/quota de volume, monitorar uso e operar purge após cópia confirmada ao R2. Não há coleta automática por idade no MVP.

Código e arquivos de entrada permanecem no registro até purge. Não armazenar segredos nos inputs. Remover temporários de staging após execução; arquivos necessários a continuidade devem ser checkpoints explícitos autorizados pelo Hub.

## 6. Runner e coleta de arquivos

- Imagem de análise aceita apenas digest ou ID sha256 já presente no host. Não fazer pull na execução.
- `--runtime=runsc` fixo, sem fallback.
- Rede none, rootfs read-only, usuário não root, capabilities removidas e no-new-privileges.
- Inputs bind-mounted read-only em `/input`. Diretório absoluto do spool deve ser igual no host Docker e no worker.
- Trabalho em tmpfs `/workspace`. Sem volume gravável do host exposto ao sandbox.
- Entry point fixo supervisiona Python, limita stdout e mantém o container vivo apenas para coleta.
- Outputs copiados por arquivo tar enquanto tmpfs existe; apenas arquivos regulares com nomes simples. Rejeitar traversal, symlinks, hardlinks, dispositivos, duplicados, subdiretórios e excesso de tamanho.
- Destruir container ao final, em cancelamento ou timeout. Falha de limpeza bloqueia purge e fica registrada; limpeza é retomada no startup.
- Startup só prossegue após verificar runtime/imagem e limpar containers marcados do serviço. Use uma instalação por daemon/spool.

Esses controles foram implementados, mas isolamento real só é considerado homologado após testes com Docker/runsc na VPS. Testes unitários não demonstram resistência a escape.

## 7. Análise de dados e PDFs

MVP executa código livre dentro do sandbox e entrega arquivos. Inclui exemplo de comparação de CSVs normalizados com Decimal e geração XLSX.

Não há parser universal de DRE nem serviço automático de extração de tabelas implementado. pdfplumber está na imagem; o código de análise pode usá-lo, mas extração precisa de validação por layout, páginas, sinais, linhas, períodos e reconciliação de totais. PDF digitalizado exige OCR ou capacidade multimodal separada.

Calcular em Python não garante regra contábil correta. Não somar detalhes e subtotais juntos; justificar base de AV/AH; conservar ordem original quando solicitado. Valores financeiros devem usar Decimal ou centavos inteiros, com arredondamento explícito. Exportação para Excel deve tratar strings que poderiam virar fórmulas.

O modelo pode receber esquema/amostras/resumo; não é verdade que o uso do executor elimina todo tráfego de dados ou tokens para o provedor. Limitar retorno e registrar fontes/metodologia no Hub.

## 8. Integração futura com o Assistant View

Não implementada neste repositório, pois o código atual do Hub não está disponível neste ambiente.

1. Registrar ferramenta `run_analysis` no adapter de modelos que suportam tool calling.
2. Autorizar tenant/conversa/arquivos; conferir quota e capacidade do módulo.
3. Baixar bytes do R2 no Hub, montar entradas limitadas e chamar API privada. Não passar credenciais R2 ou DSN ao sandbox.
4. Persistir job ID e chave de idempotência na geração.
5. Acompanhar estado sem manter conexão SQL ou transação aberta.
6. Ao concluir, baixar artefatos, conferir tamanho/hash, salvar via mecanismo existente no R2 e registrar origem/permissão.
7. Enviar resumo controlado ao modelo; manter frontend do Hub em sua experiência de streaming.
8. Após cópia/verificação, purgar spool conforme política de retenção.

O polling S2S do job não substitui streaming da resposta ao usuário. Adicionar ao Hub o botão Continuar quando houver progresso, orçamento e quota. Falhas repetidas idênticas interrompem o ciclo; não aprovar loop improdutivo.

Checkpoint deve conter mensagens/tool calls, resultados necessários, referências dos arquivos e versões. Apenas resumo pode ser insuficiente para retomar. Não depender de estado Python na RAM. Nova etapa recebe checkpoints autorizados como inputs de um sandbox novo.

Consentimento para continuar não altera quota mensal, isolamento, timeout por execução ou permissão de dados. Estimativa de custo usa preço versionado e tokens efetivos; não embutir preços não verificados nesta spec.

## 9. Implantação inicial na mesma VPS

API e worker entram no Compose/Dokploy. Sandbox é iniciado pelo Docker do host com runsc. Instalar/configurar gVisor no host e testar suporte antes de habilitar a ferramenta no Hub.

Não automatizar restart do daemon Docker do host em script de deploy: esse restart afeta Hub, banco e outros serviços. Agendar e executar manualmente após revisão das configurações existentes.

API sem porta pública; exemplo publica apenas localhost. Para Hub em outro Compose, criar rede privada de controle com API/Hub, sem ligar PostgreSQL ou sandboxes a ela. Worker não precisa de rede externa. API/worker compartilham apenas spool; só worker recebe Docker socket.

Configurar token, GID do socket, caminho/ownership do spool e digest da imagem. Fixar imagens de produção por digest após build e homologação. Não usar perfil privilegiado nem montar raiz do host no sandbox.

Disponibilidade do gVisor, cgroups e flags deve ser verificada na VPS. Falha não autoriza fallback para runc. Migração a VPS dedicada mantém contrato HTTP, troca endpoint/rede e não exige mudar os modelos de IA.

## 10. Critérios de aceitação

- Autenticação obrigatória, tenants isolados, IDs adulterados recusados, corpo limitado e idempotência sob concorrência.
- Persistência de estados, recuperação após reinício e cancelamento sem resultado concluído falso.
- API sem Docker socket; sandbox sem rede, segredos e escrita nas entradas.
- Sem fallback quando runsc/imagem ausentes.
- Coleta de arquivos sem traversal, symlink, hardlink ou excesso de tamanho.
- Timeout e limpeza reais; não deixar containers indefinidamente.
- Homologar excesso de CPU/memória/processos, stdout gigante, tentativa de rede e leitura host, arquivo enorme e saída malformada.
- Comparativo de fixture contábil conhecida com resultado conferido e XLSX que abre.
- Integração com Hub só pronta após teste real com provider e R2, preservando permissões e quotas.

## 11. Fases e status inicial

| Fase | Entrega | Estado nesta entrega |
| --- | --- | --- |
| 1 | API, persistência, worker, runner e testes | Código inicial implementado e testes locais registrados em VALIDATION.md |
| 2 | Build Docker/runsc e teste de isolamento na VPS | Pendente; sem acesso ao servidor nesta sessão |
| 3 | Ferramenta no Hub, R2 e checkpoints | Pendente; exige repositório do Hub |
| 4 | Importação PDF estruturada e análises DRE validadas | Evolução específica, não promessa automática do sandbox |
| 5 | Continuação autorizada, quotas e UX no Hub | Pendente de integração |

Não apresentar toda a solução como pronta apenas porque a API funciona. Não implantar sem revisar os recursos e a operação da VPS compartilhada.

## 12. Fontes técnicas consultadas

- https://gvisor.dev/docs/user_guide/quick_start/docker/
- https://gvisor.dev/docs/architecture_guide/intro/
- https://docs.docker.com/reference/compose-file/services/
- https://github.com/jsvine/pdfplumber
- https://developers.openai.com/api/docs/guides/tools-code-interpreter

Reconsultar versões e compatibilidade na execução. Capacidades de provedores externos são independentes desta API de análise.
