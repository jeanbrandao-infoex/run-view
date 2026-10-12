# Infoex Run

Leia docs/spec.md e README.md. Serviço independente, não é um consumidor do SDK do Hub.
API e worker são processos distintos. Nunca execute Python no host nem faça fallback de runsc para runc.
Não exponha Docker socket à API ou ao sandbox. Não adicione fetch de URLs livres ou mounts definidos pelo cliente.
Não registre código, arquivos ou tokens em logs operacionais. Preserve separação tenant/job e os limites.
Execute go test -race ./..., go vet ./..., go build ./... e a homologação real descrita no README quando houver Docker/runsc.
Mudanças no isolamento precisam de testes reais, não apenas mocks. Não publique nem altere a VPS sem autorização específica.
