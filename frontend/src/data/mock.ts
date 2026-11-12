// Placeholder data for modules whose backend arrives in later phases (chat, calls, directory, announcements).
import { avatarColors } from '../theme/tokens';
import { initials } from '../lib';

export type Presence = 'on' | 'busy' | 'meet' | 'away' | 'dnd';
export type Line = 'on' | 'busy' | 'off';

export interface Person { id: number; name: string; role: string; dept: string; site: string; ext: string; line: Line; color: string; ini: string }
export interface Msg { from: 'me' | 'them' | number; text: string; time: string }
export interface Chat { id: string; person?: number; group?: string; members?: number[]; unread: number; msgs: Msg[] }
export interface CallLog { person: number; kind: 'miss' | 'in' | 'out'; when: string }
export interface Announcement { id: number; urgent?: boolean; title: string; tag: string; by: string; date: string; body: string; ack: boolean }

const raw: Omit<Person, 'color' | 'ini'>[] = [
  { id: 1, name: 'Grace Mushi', role: 'Responsable Crédit', dept: 'Crédit', site: 'Siège — Dar es Salaam', ext: '2104', line: 'on' },
  { id: 2, name: 'Joseph Kimaro', role: 'Ingénieur Réseau', dept: 'DSI', site: 'Siège — Dar es Salaam', ext: '1002', line: 'on' },
  { id: 3, name: 'Fatma Said', role: "Directrice d'agence", dept: "Réseau d'agences", site: 'Agence Zanzibar', ext: '3401', line: 'busy' },
  { id: 4, name: 'Peter Mrema', role: 'Analyste Conformité', dept: 'Conformité', site: 'Siège — Dar es Salaam', ext: '2210', line: 'on' },
  { id: 5, name: 'Neema Lyimo', role: 'Chargée RH', dept: 'Ressources Humaines', site: 'Siège — Dar es Salaam', ext: '2050', line: 'off' },
  { id: 6, name: 'David Massawe', role: 'Trésorier', dept: 'Trésorerie', site: 'Siège — Dar es Salaam', ext: '2301', line: 'on' },
  { id: 7, name: 'Rehema Ally', role: 'Caissière principale', dept: "Réseau d'agences", site: 'Agence Arusha', ext: '3612', line: 'on' },
  { id: 8, name: 'Support IT', role: 'Service Desk', dept: 'DSI', site: 'Siège — Dar es Salaam', ext: '1000', line: 'on' },
  { id: 9, name: 'Hassan Juma', role: 'Chef de projet digital', dept: 'Banque digitale', site: 'Siège — Dar es Salaam', ext: '2405', line: 'on' },
  { id: 10, name: 'Agnes Shirima', role: 'Gestionnaire Risques', dept: 'Risques', site: 'Agence Mwanza', ext: '3820', line: 'busy' },
];
export const people: Person[] = raw.map((p, i) => ({ ...p, color: avatarColors[i % avatarColors.length], ini: initials(p.name) }));
export const person = (id: number) => people.find((p) => p.id === id)!;

export const initialChats = (): Chat[] => [
  { id: 'c1', person: 1, unread: 2, msgs: [
    { from: 'them', text: 'Bonjour, as-tu reçu le dossier de crédit de la société Kilimanjaro Traders ?', time: '09:12' },
    { from: 'me', text: 'Oui, je le relis ce matin.', time: '09:20' },
    { from: 'them', text: 'Parfait. Le comité se réunit à 14h, il nous faut ton avis avant.', time: '10:41' },
    { from: 'them', text: "Tu peux m'appeler sur mon poste 2104 si besoin.", time: '10:42' }] },
  { id: 'c2', group: 'DSI — Projet EximPhone', members: [2, 9, 8], unread: 1, msgs: [
    { from: 2, text: 'Les 20 premiers postes Yealink sont configurés au 3e étage.', time: '08:30' },
    { from: 9, text: "Super ! L'appli PC est prête pour le pilote.", time: '08:45' },
    { from: 'me', text: 'Je teste les appels poste ↔ PC cet après-midi.', time: '09:02' },
    { from: 8, text: "N'oubliez pas : les comptes sont synchronisés avec l'AD chaque nuit.", time: '11:05' }] },
  { id: 'c3', person: 3, unread: 0, msgs: [
    { from: 'them', text: "Le distributeur de l'agence Zanzibar est de nouveau en service.", time: 'Hier' },
    { from: 'me', text: 'Merci Fatma, bonne nouvelle !', time: 'Hier' }] },
  { id: 'c4', person: 6, unread: 0, msgs: [{ from: 'them', text: 'Position de trésorerie envoyée par e-mail.', time: 'Lun.' }] },
];

export const initialCalls = (): CallLog[] => [
  { person: 1, kind: 'miss', when: '10:38' }, { person: 2, kind: 'out', when: '09:55' },
  { person: 3, kind: 'in', when: 'Hier 16:20' }, { person: 6, kind: 'out', when: 'Hier 11:02' }, { person: 8, kind: 'in', when: 'Lun. 08:47' },
];

export const initialAnnouncements = (): Announcement[] => [
  { id: 1, urgent: true, title: 'Maintenance du core banking — samedi 22h à 02h', tag: 'Urgent', by: "Direction des Systèmes d'Information", date: "Aujourd'hui, 08:00",
    body: 'Les opérations en agence seront suspendues pendant la fenêtre de maintenance. Merci d\'informer vos clients et de clôturer vos caisses avant 21h30.', ack: false },
  { id: 2, title: 'Lancement du pilote EximPhone', tag: 'Projet', by: 'Direction Générale', date: 'Hier, 15:30',
    body: 'À partir de lundi, le siège bascule sur EximPhone : un seul numéro interne sur votre poste de bureau et votre PC. Les agences suivront au prochain trimestre.', ack: false },
  { id: 3, title: 'Rappel : formation anti-blanchiment (AML) obligatoire', tag: 'Conformité', by: 'Département Conformité', date: 'Lun., 09:00',
    body: 'Le module e-learning AML doit être complété avant le 30 novembre. Contactez le poste 2210 pour toute question.', ack: true },
];
