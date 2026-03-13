# WhatsMiau - Evolution API clone em Go

## Contexto do Projeto
Estamos construindo uma réplica da Evolution API em Go (WhatsMiau).

## Referência principal
Implementação original em TypeScript:
https://raw.githubusercontent.com/EvolutionAPI/evolution-api/refs/heads/main/src/api/integrations/chatbot/chatwoot/services/chatwoot.service.ts

## Build
docker build -t whatsmiau-custom:v4 . && date

## Stack
- Go (whatsmeow)
- Chatwoot integration
- Evolution API compatible

## Regras para Git / GitHub

IMPORTANT: Antes de qualquer `git push` ou criação de PR, SEMPRE:
1. Perguntar ao usuário: "Deseja enviar ao GitHub agora?"
2. Se sim, perguntar: "Para qual branch? (ex: main, v10, feature/xxx)"
3. Só executar o push após confirmação explícita com o nome da branch

IMPORTANT: Nunca fazer push automático sem confirmação. Nunca assumir branch padrão.

### Arquivos sensíveis — nunca commitar
- `.env` e qualquer variante (`.env.local`, `.env.production`, etc.)
- `docker-compose.yml` (usar `docker-compose.example.yml` como referência)
- Arquivos `*.json` com credenciais, `*.pem`, `*.key`
- Diretório `data/`
