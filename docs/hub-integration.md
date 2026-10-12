# Integração a implementar no Hubview

Este arquivo orienta o agente do Hub; não implica que o código do Hub já foi alterado.

1. Leia o contrato de módulos e a spec do Assistant View.
2. Adicione `ANALYSIS_SERVICE_URL` e referência segura de `ANALYSIS_SERVICE_TOKEN` na configuração do backend. Token somente servidor.
3. Implemente um cliente HTTP no domínio dono da execução de análises; use `internal` apenas para composição/configuração, sem pacote genérico.
4. Registre ferramenta `run_analysis` com schema de argumentos: código e IDs de arquivos autorizáveis. Nunca permitir DSN, URL arbitrária, tenant escolhido pelo modelo ou flags de container.
5. Resolva tenant pela geração validada, confira vínculo/conversa/módulo/quota e leia os arquivos autorizados do R2. Envie base64 para o executor respeitando 24 MiB no total; limite maior exige evolução por protocolo de uploads controlado.
6. Persista job ID, chave de idempotência, estado e fonte dos inputs na geração. Repetição de chamada HTTP reutiliza chave; uma tentativa de análise diferente ganha nova chave.
7. Acompanhe o job por worker do Hub, sem capturar DB de uma requisição concluída.
8. Revalide acesso ao entregar. Baixe outputs, confira hashes/tamanhos e grave artefatos no R2 com permissões da conversa. HTML permanece isolado.
9. Entregue ao modelo apenas resumo e manifesto necessários; registre tool call/result e checkpoints, sem serializar todas as linhas se não necessário.
10. Confirme envio ao R2 antes do purge. Em falha de cópia, retenha job e permita retry de download, sem executar novamente a análise.

O botão Continuar pede autorização para mais uma tranche de trabalho. Ele não altera quotas, limites do sandbox ou acesso. Detectar repetições de ferramenta sem progresso e interromper. Valor estimado deve ser baseado em preços verificados e consumo registrado, não em constante desta spec.

Nova execução pode receber outputs autorizados da anterior como inputs. Essa é a continuidade inicial; não guardar um container vivo indefinidamente nem resumir de forma que se percam dados necessários à retomada.

Critérios: resultado real de DRE/CSV conferido, isolamento tenant/objeto, token ausente no browser, recuperação após falha HTTP, quota sob concorrência, cancelamento e R2 sem artefato falso. Não declarar a ferramenta pronta somente por testar POST no executor.
