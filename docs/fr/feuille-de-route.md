# Exim Connect — Feuille de route et envoi de courriels externes

*Ce qui reste à faire avant la mise en production, ce qui peut venir ensuite, et ce qu'il nous faut pour envoyer et recevoir des courriels à l'extérieur de la banque.*

---

## 1. Où en est-on ?

| Domaine | État |
|---|---|
| Pointage, messagerie, appels, annonces, RH, audit | **Terminés et testés** |
| Courrier **interne** (To, Cc, Cci, brouillons, fils, pièces jointes, recherche) | **Terminé et testé** |
| Courrier **externe** (écrire à l'extérieur, recevoir des réponses) | **Développé et testé** avec un serveur de messagerie simulé. **Il manque seulement le raccordement au vrai serveur de la banque** (§ 2) |
| Téléphones IP de bureau (SIP) | **Terminés** ; à valider avec les modèles réels de la banque |
| Réunions vidéo jusqu'à 12, partage d'écran | **Terminés et testés** |
| Prise de contrôle à distance (application PC) | **Terminée** ; la fluidité et l'ergonomie s'ajustent avec les premiers utilisateurs |
| Application PC (installateur Windows) | **Disponible** ; il manque la **signature de code** pour la production (§ 3) |
| Environnement de test (serveur dédié) | **En place** : certificat de test, annuaire fictif, accès limité |

---

## 2. Envoyer des courriels externes : ce qu'il nous faut

### 2.1 Comment ça marche

Exim Connect **n'est pas un serveur de messagerie** et n'a pas vocation à le remplacer. Pour écrire à l'extérieur, il s'appuie sur **le serveur de messagerie existant de la banque**, dont il se sert comme d'une passerelle :

```
   SORTANT                                         ENTRANT
   Collaborateur ─► Exim Connect ─► relais SMTP     Internet ─► serveur de messagerie
                                    de la banque                 de la banque
                                         │                            │ règle de copie
                                         ▼                            ▼
                                     Internet                    boîte aux lettres
                                                                  de service (IMAP)
                                                                      │ lecture toutes les 30 s
                                                                      ▼
                                                              Exim Connect range chaque
                                                              message dans la boîte du
                                                              collaborateur concerné
```

**Pourquoi passer par le serveur existant ?** Les courriels partent avec **l'identité de la banque** (signature DKIM, SPF, DMARC), traversent **l'antivirus, l'anti-spam et la passerelle de sécurité** habituels, sont **archivés** comme tout courriel de la banque, et la réputation de l'adresse d'expédition n'est pas mise en danger.

### 2.2 Ce que la banque doit nous fournir

À remplir par l'équipe de messagerie (DSI / administrateur Exchange ou équivalent).

#### A. Pour **envoyer** (relais SMTP)

| N° | Information ou action | Exemple | Pourquoi |
|---|---|---|---|
| A1 | **Nom ou adresse** du serveur d'envoi (relais SMTP) | `smtp.exim.example` | où Exim Connect dépose les messages |
| A2 | **Port** et **mode de sécurité** | `587` + STARTTLS, ou `465` + TLS | chiffrement de la liaison |
| A3 | **Compte d'envoi** (identifiant + mot de passe) **ou autorisation par adresse IP** du serveur Exim Connect | `svc-eximconnect` | authentification du relais |
| A4 | **Droit d'envoyer au nom des collaborateurs** (« Send As »), **ou** une adresse unique d'expédition | `noreply-connect@exim.example` | voir la décision 1 (§ 2.3) |
| A5 | **Certificat de l'autorité interne** de la banque (fichier .pem) | `ca-exim.pem` | pour que la liaison chiffrée soit vérifiée |
| A6 | **Taille maximale** d'un message du relais | 25 Mo | doit être ≥ aux pièces jointes autorisées |
| A7 | **Ouverture du pare-feu** : du serveur Exim Connect vers le relais, sur le port choisi | — | la liaison doit être autorisée |

#### B. Pour **recevoir** (boîte aux lettres de service)

| N° | Information ou action | Exemple | Pourquoi |
|---|---|---|---|
| B1 | **Une boîte aux lettres dédiée** à Exim Connect | `connect-entrant@exim.example` | Exim Connect la relève |
| B2 | **Serveur IMAP**, port et mode de sécurité | `imap.exim.example`, `993` + TLS | lecture de la boîte |
| B3 | **Compte** (identifiant + mot de passe) pour cette boîte | `svc-eximconnect-imap` | authentification |
| B4 | **Une règle de copie** : le courrier des collaborateurs qui utilisent Exim Connect est **copié (Cci) ou transféré** vers cette boîte | règle de transport ou alias | c'est ce qui alimente la boîte |
| B5 | **Ouverture du pare-feu** : du serveur Exim Connect vers le serveur IMAP | — | la liaison doit être autorisée |

> Exim Connect retrouve le destinataire d'après les adresses dans les en-têtes du message (À, Cc, `Delivered-To`, `X-Original-To`). Il suffit donc d'**un seul compte** ; aucun mot de passe de collaborateur n'est stocké.

#### C. Dans l'**annuaire d'entreprise**

| N° | Information ou action | Pourquoi |
|---|---|---|
| C1 | L'attribut **`mail`** (adresse de courriel) est **renseigné et exact** pour chaque collaborateur | sert à reconnaître les messages entrants et à construire l'expéditeur |
| C2 | Les **alias** éventuels (ancienne adresse, adresse courte) | éviter des messages « sans destinataire reconnu » |

#### D. Côté sécurité et conformité

| N° | Point | À confirmer |
|---|---|---|
| D1 | **SPF** du domaine : le serveur Exim Connect est-il autorisé à envoyer ? | uniquement nécessaire si l'expéditeur n'est pas celui du relais |
| D2 | **DKIM / DMARC** : la signature est faite par le relais habituel | confirmer |
| D3 | **Archivage / conservation légale** des courriels sortants et entrants | passe par le relais existant ? |
| D4 | **Prévention de fuite de données (DLP)** et passerelle anti-virus | s'applique au relais existant ? |
| D5 | **Domaines autorisés ou interdits** (liste blanche, liste noire) | à configurer éventuellement |

### 2.3 Décisions à prendre

| Décision | Choix possibles | Notre conseil |
|---|---|---|
| **Décision 1 : qui apparaît comme expéditeur ?** | **Adresse du collaborateur** (`prenom.nom@exim.example`) : plus naturel, mais le compte d'envoi doit avoir le droit « Send As ». **Adresse unique** (`noreply-connect@…`) avec « Répondre à » = le collaborateur : plus simple à autoriser, moins naturel. | **Adresse du collaborateur** si la messagerie l'autorise : les clients reconnaissent la banque et les réponses reviennent à la bonne personne. |
| **Décision 2 : qui peut écrire à l'extérieur ?** | Tous les collaborateurs, ou seulement certains services ou rôles. | Tous au départ, avec la **traçabilité** déjà en place (chaque envoi est journalisé, sans son contenu). |
| **Décision 3 : taille des pièces jointes** | 25 Mo par message aujourd'hui (20 Mo par fichier). | À aligner sur la limite du relais (A6). |

### 2.4 Ce que fait Exim Connect de son côté

Une fois les informations reçues, **nous** :

1. renseignons les paramètres du service (`MAIL_SMTP_*`, `MAIL_IMAP_*`, certificat) sans modifier le code ;
2. redémarrons le service API ;
3. testons : envoi vers une adresse externe, réception d'une réponse, pièce jointe, accent, Cc/Cci, échec de remise ;
4. déroulons la recette (§ 2.5) avec deux ou trois collaborateurs pilotes.

**Fonctionnement déjà couvert par les tests** (22 vérifications) : messages MIME en UTF-8 avec pièces jointes, destinataires en Cci hors enveloppe visible, reprise après échec (nouvelles tentatives à 1 min, 5 min, 30 min, 2 h, 6 h), message « Échec de remise » à l'expéditeur après un refus définitif, adresse d'un collègue remise **en interne** sans passer par le relais, courriels entrants rangés dans la bonne boîte avec leurs pièces jointes, courriels HTML convertis en texte sûr, doublons ignorés, réponses regroupées dans le même fil.

### 2.5 Recette (test d'acceptation)

| # | Test | Résultat attendu |
|---|---|---|
| 1 | Envoyer un message à une adresse **externe** de test | il arrive, expéditeur et objet corrects, accents lisibles |
| 2 | Envoyer avec une **pièce jointe** de 10 Mo | elle arrive intacte |
| 3 | Mettre une adresse en **Cci** | elle reçoit le message, les autres ne la voient pas |
| 4 | **Répondre** depuis l'adresse externe | la réponse apparaît dans la boîte du collaborateur, dans le même fil, avec la mention **Externe** |
| 5 | Écrire à une **adresse inexistante** | un message « Échec de remise » arrive à l'expéditeur |
| 6 | Écrire à un **collègue** avec son adresse complète | remise en interne, immédiate |
| 7 | Envoyer vers une adresse **bloquée** par la passerelle | l'échec est signalé proprement |
| 8 | Vérifier **SPF / DKIM / DMARC** (en-têtes du message reçu) | tous « pass » |

### 2.6 Délai indicatif

| Étape | Qui | Durée estimée |
|---|---|---|
| Collecte des informations (§ 2.2) et décisions | DSI / messagerie | 2 à 5 jours ouvrés |
| Ouvertures de pare-feu, création des comptes, règle de copie | DSI / sécurité | 1 à 5 jours selon le circuit de changement |
| Configuration et premiers essais | Équipe Exim Connect | 1 jour |
| Recette avec utilisateurs pilotes | DSI + pilotes | 1 à 2 jours |

### 2.7 Limites actuelles du courrier

Le courrier est en **texte brut** (pas de mise en forme ni d'images dans le corps du message). Ne sont pas encore proposés : **signatures**, **réponse automatique d'absence**, **dossiers personnalisés**, **boîtes partagées**, **invitations de calendrier**. Le **filtrage anti-spam** est laissé au serveur de la banque. Cette version utilise **une boîte de service unique** pour la réception, plutôt qu'une boîte par collaborateur.

---

## 3. Avant la mise en production

| # | Action | Détail | Responsable |
|---|---|---|---|
| 1 | **Certificat serveur** | certificat de l'autorité interne de la banque, sur le nom définitif | DSI |
| 2 | **Annuaire réel** | raccordement en LDAPS, compte de service en lecture seule, import des attributs (nom, service, site, poste, `mail`, manager) | DSI |
| 3 | **Plages d'adresses réelles** | remplacer les plages de démonstration par les réseaux de chaque agence | DSI / réseau |
| 4 | **Horaires et jours** | vérifier horaires, tolérance et fuseau par site | RH |
| 5 | **Rôles** | désigner administrateurs IT, RH, managers, communication, auditeurs, responsables de service | DSI / RH |
| 6 | **Signature de l'installateur** | certificat de signature de code pour éviter l'avertissement de Windows | DSI |
| 7 | **Sauvegardes planifiées** | script fourni (chiffré) : le planifier chaque jour, copier hors du serveur, tester la restauration chaque mois | DSI |
| 8 | **Conservation des clés** | `MESSAGE_KEY`, `RECORDING_KEY`, `SIP_KEY`, phrase de sauvegarde : coffre séparé du serveur | DSI / sécurité |
| 9 | **Pare-feu** | ouvrir uniquement HTTPS, et les ports de téléphonie et de média nécessaires ; accès d'administration restreint | réseau |
| 10 | **Supervision** | alertes sur la santé des services, l'espace disque, la sauvegarde | DSI |
| 11 | **Durée de conservation** | pointage, audit, enregistrements : durées légales | juridique / RH |
| 12 | **Test de charge réel** | sur le serveur de production, avec le nombre réel d'utilisateurs | équipe projet |
| 13 | **Formation et guide** | présenter le manuel aux collaborateurs, désigner des relais | RH / DSI |
| 14 | **Recette métier** | 2 ou 3 semaines avec un groupe pilote (une agence + le siège) | tous |

---

## 4. Évolutions possibles

### À court terme (1 à 3 mois)

| Évolution | Intérêt |
|---|---|
| **Synchronisation nocturne de l'annuaire** (arrivées, départs, désactivation de comptes) | comptes toujours à jour sans action manuelle |
| **Calendrier des jours fériés** et congés | pas de fausses absences ; rapports RH justes |
| **Notification du responsable** (courriel ou message) en cas d'oubli de pointage de départ | suivi sans consulter le tableau |
| **Limiter qui peut demander une prise de contrôle** (par rôle ou service) | un cadre de sécurité plus strict |
| **Prise de contrôle plus fluide** : retours des premiers essais, qualité d'image et réactivité | confort d'usage |

### À moyen terme (3 à 9 mois)

| Évolution | Intérêt |
|---|---|
| **Authentification forte (2 facteurs)** et **connexion unique (SSO)** | sécurité renforcée |
| **Messagerie vocale** et renvois d'appel | téléphonie complète |
| **Raccordement à un opérateur téléphonique** (appels vers l'extérieur) | un seul téléphone |
| **Courrier enrichi** : signatures, réponse d'absence, dossiers personnalisés, boîtes partagées | usage quotidien proche d'un client lourd |
| **Enregistrement et résumé des réunions**, discussion dans la réunion | trace et gain de temps |
| **Transfert de fichiers et presse-papiers** pendant une prise de contrôle | assistance à distance plus efficace |
| **Prise en charge de plusieurs écrans** pour la prise de contrôle | postes à deux écrans |

### À plus long terme

| Évolution | Intérêt |
|---|---|
| **Application mobile** (iOS, Android) | joignable hors du poste |
| **Portail de demandes** étendu (congés, attestations, notes de frais) à partir des tickets | un guichet unique |
| **Statistiques d'usage** (tableaux de bord de la DSI) | pilotage |
| **Version swahili** et autres langues | accessibilité |

---

## 5. Limites connues

- Un jour sans pointage compte comme **absence** : week-ends et jours fériés ne sont pas distingués (voir *calendrier des jours fériés*).
- La **prise de contrôle** ne fonctionne que sous **Windows**, depuis l'**application PC**, pour un **écran entier**.
- L'application PC n'est pas encore **signée** : Windows demande une confirmation à l'installation.
- Le courrier externe suppose **le serveur de messagerie de la banque** et la règle de copie décrite au § 2.2.
- Les tests de montée en charge ont été faits sur un petit serveur : une **mesure sur le matériel de production** reste à faire.
