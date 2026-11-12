# PROMPT — Développement de la plateforme EximPhone (Docker)

> À coller dans un assistant de développement (Claude Code, etc.) à la racine d'un dépôt vide.
> Le prototype HTML validé (`prototype/eximphone.html`) et les ressources de la charte (`brand/`) doivent être placés dans le dépôt avant de lancer le prompt.

---

## 1. Rôle et mission

Tu es un ingénieur logiciel senior spécialisé en téléphonie IP (SIP/RTP/WebRTC), en applications temps réel et en sécurité bancaire.

Ta mission : développer **EximPhone**, la plateforme de communication interne et de pointage de présence des employés d'**Exim Bank**. Tout le projet est **conteneurisé avec Docker** et se lance avec une seule commande `docker compose up`.

Le prototype `prototype/eximphone.html` est la **référence fonctionnelle et visuelle** : reproduis ses écrans, ses parcours et sa charte graphique.

---

## 2. Contraintes non négociables

1. **Aucun logiciel tiers en tant que service** : pas d'Asterisk, FreePBX, 3CX, Rocket.Chat, Mattermost, Matrix, Twilio, Firebase ni aucun SaaS. Tout le métier (SIP, médias, chat, présence, pointage) est **développé par nous**.
   - Autorisé : bibliothèques open source intégrées au code (ex. `emiago/sipgo`, `pion/webrtc`, `pion/turn`, `pion/rtp`), et les briques d'infrastructure standards : PostgreSQL, Redis, Nginx, MinIO (stockage de fichiers).
2. **Auto-hébergé** : tout tourne sur les serveurs d'Exim Bank, sans aucune dépendance Internet en production.
3. **Normes ouvertes obligatoires** pour la compatibilité avec les téléphones IP du marché : SIP (RFC 3261), SDP (RFC 4566), RTP/RTCP (RFC 3550), SRTP (RFC 3711), digest auth (RFC 8760), codecs **G.711 µ-law/A-law** (obligatoire) et **Opus** (PC).
4. **Sécurité bancaire** : TLS partout, SIP over TLS (port 5061), SRTP, mots de passe jamais en clair, journal d'audit immuable, principe du moindre privilège.
5. Le code, les commentaires et la documentation technique sont en **anglais**. L'interface utilisateur est en **français**, avec une structure i18n prête pour l'anglais et le swahili.

---

## 3. Stack technique imposée

| Couche | Technologie |
|---|---|
| Backend (API, chat, présence, pointage) | **Go 1.23+**, framework `chi` ou `echo`, `pgx`, `sqlc`, migrations `golang-migrate` |
| Serveur SIP (registrar + proxy B2BUA) | **Go** + `emiago/sipgo` |
| Serveur média (relais RTP, WebRTC ↔ SIP, enregistrement) | **Go** + `pion/webrtc`, `pion/rtp`, `pion/srtp` |
| TURN/STUN interne | **Go** + `pion/turn` (service maison) |
| Temps réel client | WebSocket (`nhooyr.io/websocket` ou `gorilla/websocket`) |
| Base de données | **PostgreSQL 16** |
| Cache, présence, pub/sub | **Redis 7** |
| Fichiers et enregistrements | **MinIO** (compatible S3) |
| Frontend web | **React 18 + TypeScript + Vite**, Zustand, TanStack Query |
| Application bureau (PC) | **Electron** enveloppant le frontend (Windows en priorité) |
| WebRTC / SIP côté client | **SIP over WebSocket (RFC 7118)** avec `sip.js`, ou une signalisation JSON maison relayée par le serveur média |
| Reverse proxy / TLS | **Nginx** |
| Annuaire | **LDAP / Active Directory** (`go-ldap/ldap`) |
| Observabilité | Logs JSON structurés, endpoint `/metrics` Prometheus, healthchecks |
| Tests | `go test` + `testcontainers-go`, Vitest + Playwright (front), SIPp pour la charge SIP |

---

## 4. Architecture Docker

Crée un `docker-compose.yml` (plus `docker-compose.dev.yml` et `docker-compose.prod.yml`) avec les services suivants :

| Service | Rôle | Ports exposés |
|---|---|---|
| `nginx` | Reverse proxy TLS, sert le frontend web, proxy `/api` et `/ws` | 443 |
| `api` | API REST + WebSocket : auth, pointage, chat, présence, annuaire, annonces, admin, RH | interne 8080 |
| `sip` | Registrar SIP, proxy, routage des postes, renvois, ne pas déranger, forking poste + PC | 5060/udp, 5060/tcp, 5061/tcp (TLS), 8089 (WSS) |
| `media` | Relais RTP/SRTP, pont WebRTC ↔ SIP, transcodage Opus ↔ G.711, enregistrement d'appels | 10000-10200/udp |
| `turn` | STUN/TURN maison (pion/turn) pour les PC derrière NAT | 3478/udp |
| `provisioning` | Configuration automatique des téléphones IP (Yealink, Grandstream, Fanvil) via HTTPS | interne, servi via nginx `/prov` |
| `worker` | Tâches planifiées : synchro AD nocturne, clôture des pointages non pointés, rapports RH, purge, sauvegardes | — |
| `postgres` | Base de données (volume persistant) | interne |
| `redis` | Présence temps réel, sessions, pub/sub entre `api` et `sip` | interne |
| `minio` | Pièces jointes du chat, enregistrements d'appels, exports | interne |
| `ldap-mock` (dev uniquement) | OpenLDAP de test avec 20 employés fictifs | interne |

Exigences Docker :
- Un **Dockerfile multi-stage** par service Go : build `golang:1.23-alpine`, puis image finale `gcr.io/distroless/static` ou `alpine`, avec un **utilisateur non-root**.
- Un **healthcheck** sur chaque service et des `depends_on` avec `condition: service_healthy`.
- **Réseaux séparés** : `front` (nginx), `back` (api, sip, media, worker) et `data` (postgres, redis, minio). La base n'est jamais exposée.
- Les secrets passent par **Docker secrets** ou un fichier `.env` non versionné. Fournis un `.env.example`.
- Les **volumes nommés** pour postgres, minio et les certificats.
- Pour `media`, utilise `network_mode: host` en production ou une plage de ports UDP explicite, et documente pourquoi (RTP et NAT).
- Un **Makefile** avec : `make dev`, `make up`, `make down`, `make logs`, `make migrate`, `make seed`, `make test`, `make certs` (certificats auto-signés pour le dev), `make backup`.

---

## 5. Fonctionnalités à développer

### 5.1 Authentification et pointage de présence (obligatoire)
- La connexion se fait avec les identifiants **Active Directory** (bind LDAP). En dev, utilise le `ldap-mock`.
- **La connexion = le pointage d'arrivée.** Impossible d'accéder à l'application sans pointer.
- **La déconnexion = le pointage de départ**, après un écran de confirmation qui récapitule l'arrivée, le départ et la durée.
- Le pointage n'est accepté que depuis les **plages IP du réseau interne** (configurables par site : siège, agences). Hors réseau, la connexion est refusée avec un message clair.
- Règles configurables : heure de début (08:00), tolérance (15 min), heure de fin (17:00). Calcul automatique : **À l'heure, En retard, Départ anticipé (motif obligatoire), Départ non pointé**.
- Si l'employé ne pointe pas son départ, le `worker` clôture la journée à minuit avec l'heure de la **dernière activité** et marque « Départ non pointé ». Le manager reçoit une notification.
- Sessions : JWT d'accès court (15 min) et refresh token en cookie httpOnly. Révocation immédiate si le compte est désactivé dans AD.
- Une seule journée de travail ouverte par employé, même s'il est connecté sur plusieurs appareils.

### 5.2 Statut de présence
- Statuts : **Disponible, Occupé, En réunion, Absent, Ne pas déranger, Hors ligne** (ce dernier est automatique après le pointage de départ).
- Le statut est diffusé en temps réel à tous les clients (WebSocket + Redis pub/sub).
- Le serveur SIP applique les effets du statut sur les appels :
  - **Ne pas déranger** : rejet `486 Busy Here` et renvoi vers la messagerie vocale.
  - **Absent** : renvoi vers le poste de délégation configuré.
  - **En réunion** : le client PC sonne sans bruit.
  - Synchronisation du DND avec le téléphone IP quand le modèle le permet (event package `as-feature-event`).
- Passage automatique en « Occupé » pendant un appel, puis retour au statut précédent.

### 5.3 Téléphonie
- Chaque employé a un **numéro interne à 4 chiffres** (ex. 2088), utilisable sur son **poste de bureau IP** et dans son **application PC**.
- **Forking** : un appel entrant sonne sur tous les appareils actifs. Quand l'un décroche, les autres reçoivent `CANCEL`.
- Fonctions : appel, raccrocher, mise en attente, muet, **transfert aveugle et supervisé** (REFER), DTMF (RFC 4733), journal d'appels, appels manqués, messagerie vocale (dépôt et écoute dans l'application).
- Pont **WebRTC (PC) ↔ SIP (téléphone de bureau)** dans le service `media`, avec transcodage Opus ↔ G.711.
- **Enregistrement des appels** activable par département (exigence de conformité), stocké chiffré dans MinIO, avec accès journalisé.
- Prévois l'interface d'un **trunk SIP opérateur** pour les appels externes (désactivé par défaut) et un plan de numérotation : `0` + numéro = appel externe.
- Les comptes SIP des téléphones sont générés automatiquement depuis l'administration. Le service `provisioning` sert les fichiers de configuration par adresse MAC.
- Un **poste analogique** existant peut être raccordé via un adaptateur ATA SIP : documente la procédure.

### 5.4 Messagerie interne
- Conversations individuelles et de **groupe** (par département, agence ou projet).
- Fonctions : messages texte, pièces jointes (PDF, images, max 20 Mo, analyse du type MIME), accusés de réception (envoyé / distribué / lu), indicateur « écrit… », recherche, historique paginé.
- Chiffrement en transit (TLS) et au repos (colonne chiffrée AES-256-GCM, clé gérée hors de la base). Architecture prête pour un futur chiffrement de bout en bout.
- **Archivage réglementaire** : aucun message n'est supprimé physiquement. La suppression côté utilisateur est logique et tracée.

### 5.5 Annuaire
- Synchronisé chaque nuit avec AD : nom, poste, département, site, e-mail, manager, photo.
- Recherche instantanée, regroupement par département, fiche employé avec statut en direct, boutons **Appeler** et **Message**.

### 5.6 Annonces officielles
- Publication réservée aux rôles `communication` et `direction`.
- Niveaux **Normal** et **Urgent** (l'urgent s'affiche en popup).
- Ciblage : tous, un département ou un site.
- **Accusé de réception** obligatoire, avec un tableau de suivi « qui a lu / qui n'a pas lu ».

### 5.7 Tableau de bord RH et managers
- Vue en temps réel : présents, absents, en retard, par site et par département.
- Historique filtrable et **export Excel/CSV mensuel** pour la paie.
- Validation des motifs de départ anticipé et correction manuelle d'un pointage, avec justification et audit.
- Un manager ne voit que son équipe. Le rôle RH voit tout.

### 5.8 Administration
- Rôles (RBAC) : `employee`, `manager`, `hr`, `communication`, `it_admin`, `auditor`.
- Gestion des utilisateurs et des numéros, des appareils (MAC, modèle, poste), des sites et des plages IP, des horaires de travail, des groupes de chat et des règles d'enregistrement.
- **Journal d'audit** en ajout seul (append-only) : connexions, pointages, corrections, accès aux enregistrements, changements de droits. Consultable par le rôle `auditor`.

---

## 6. Charte graphique Exim Bank (obligatoire)

Reprends exactement le style du prototype :
- **Bleu marine principal** `#002E6C`, **bleu secondaire** `#22317F`, **or** `#A99564`, or clair `#EFE9DB`.
- Couleurs d'état : succès `#21CE99`, alerte `#F21053`, fond `#F4F6FA`, texte `#1D2433`, texte secondaire `#6B7387`.
- **Police Bliss Pro** (Regular, ExtraBold), fournie dans `brand/fonts/`.
- **Logo officiel** dans `brand/logo.svg` (fond clair) et `brand/logo-white.svg` (fond bleu), avec la signature « Innovation is life ».
- Structure de l'écran : barre latérale bleu marine avec indicateur doré sur l'onglet actif, colonne de liste blanche, zone principale claire, boutons principaux bleu marine et boutons secondaires or.
- Centralise tout dans des **design tokens** (`frontend/src/theme/tokens.ts` + variables CSS). Aucune couleur en dur dans les composants.
- Accessibilité : contraste AA, navigation au clavier, libellés ARIA.

---

## 7. Structure du dépôt

```
eximphone/
├── docker-compose.yml
├── docker-compose.dev.yml
├── docker-compose.prod.yml
├── .env.example
├── Makefile
├── README.md
├── brand/                  # logos, polices
├── prototype/              # prototype HTML de référence
├── docs/                   # architecture, déploiement, provisioning, ADR
├── services/
│   ├── api/                # Go — REST + WebSocket
│   ├── sip/                # Go — registrar/proxy SIP
│   ├── media/              # Go — RTP/WebRTC/enregistrement
│   ├── turn/               # Go — STUN/TURN
│   ├── provisioning/       # Go — config téléphones IP
│   ├── worker/             # Go — tâches planifiées
│   └── pkg/                # code Go partagé (auth, config, logger, models)
├── db/
│   ├── migrations/
│   └── seed/
├── frontend/               # React + TS + Vite
├── desktop/                # Electron
├── nginx/
└── tests/
    ├── e2e/                # Playwright
    └── sip-load/           # scénarios SIPp
```

---

## 8. Modèle de données (minimum)

Tables : `users`, `sites` (avec `ip_ranges`), `departments`, `extensions`, `devices` (type `desk_phone` ou `softphone`, MAC, modèle), `sip_registrations`, `work_schedules`, `attendance_days` (arrivée, départ, statut, durée, motif), `attendance_corrections`, `presence_status`, `conversations`, `conversation_members`, `messages`, `message_receipts`, `attachments`, `calls` (CDR), `call_recordings`, `voicemails`, `announcements`, `announcement_acks`, `roles`, `user_roles`, `audit_log`.

Fournis les migrations, les index (recherche, dates) et un **seed** de démonstration avec 20 employés, 3 sites, des conversations, des appels et 30 jours de pointages.

---

## 9. Plan de livraison par phases

Avance **phase par phase**. À la fin de chaque phase : code, tests qui passent, `docker compose up` fonctionnel, README mis à jour, puis **arrête-toi et résume** ce qui est fait avant de continuer.

| Phase | Contenu | Critères d'acceptation |
|---|---|---|
| **0. Socle** | Dépôt, Docker Compose, Makefile, Nginx TLS, Postgres, Redis, MinIO, LDAP mock, CI (lint + tests) | `make dev` démarre tout, tous les healthchecks sont verts |
| **1. Auth + pointage** | Login AD, pointage d'arrivée et de départ, contrôle des IP, règles horaires, écran Présence | Connexion refusée hors réseau ; retard calculé ; départ non pointé clôturé par le worker |
| **2. Front + charte** | Application React fidèle au prototype, tokens de la charte, Electron | Comparaison visuelle avec le prototype validée ; build Electron Windows |
| **3. Présence + chat** | Statuts temps réel, conversations, groupes, pièces jointes, accusés | Deux navigateurs échangent en temps réel ; le statut se propage en moins d'une seconde |
| **4. Appels PC ↔ PC** | Signalisation, WebRTC via `media` et `turn`, journal d'appels | Appel audio fonctionnel entre deux PC, attente, muet, transfert |
| **5. Téléphones IP** | Serveur SIP, provisioning, forking poste + PC, pont WebRTC ↔ SIP, DND et renvois | Un Yealink (ou un softphone SIP de test) appelle un PC et inversement ; tests SIPp OK |
| **6. Annonces + RH + admin** | Annonces avec accusés, tableau de bord RH, exports, RBAC, audit | Export Excel mensuel correct ; un manager ne voit que son équipe |
| **7. Durcissement** | SIP TLS + SRTP, chiffrement au repos, enregistrement d'appels, sauvegardes, tests de charge (500 utilisateurs, 50 appels simultanés), documentation d'exploitation | Scan de vulnérabilités des images sans faille critique ; restauration de sauvegarde testée |

---

## 10. Qualité et sécurité

- Couverture de tests de **70 % ou plus** sur la logique métier (pointage, présence, routage SIP).
- Validation stricte de toutes les entrées, requêtes SQL paramétrées (`sqlc`), limitation de débit sur le login et l'API.
- En-têtes de sécurité Nginx : HSTS, CSP, X-Frame-Options.
- Scan des images avec Trivy dans la CI.
- Aucun secret dans le code ni dans les images.
- Les journaux ne contiennent jamais de mot de passe, de token ni de contenu de message.
- Les horodatages sont stockés en **UTC** et affichés dans le fuseau du site (par défaut `Africa/Dar_es_Salaam`).

---

## 11. Documentation attendue

- `README.md` : prérequis, installation, `make dev`, comptes de démonstration.
- `docs/architecture.md` : schéma des services et flux d'appel SIP/WebRTC (diagrammes Mermaid).
- `docs/deployment.md` : déploiement en production, certificats, ports pare-feu, plages RTP.
- `docs/provisioning.md` : configurer un téléphone Yealink ou Grandstream et un adaptateur ATA pour poste analogique.
- `docs/attendance-rules.md` : règles de pointage pour la DRH.
- `docs/adr/` : une note par décision d'architecture importante.

---

## 12. Comment travailler

1. Commence par lire `prototype/eximphone.html` et le dossier `brand/`.
2. Propose un **plan détaillé de la phase 0** et attends ma validation.
3. Ensuite, travaille phase par phase avec des commits clairs (Conventional Commits).
4. Si une décision technique importante n'est pas couverte ici, propose deux options avec leurs avantages et inconvénients, puis demande-moi.
5. Ne remplace **jamais** une brique maison par un logiciel tiers sans mon accord explicite.
