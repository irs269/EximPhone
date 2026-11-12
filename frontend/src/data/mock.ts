// Placeholder data for modules whose backend arrives in a later phase (announcements: phase 6).

export type Presence = 'on' | 'busy' | 'meet' | 'away' | 'dnd';

export interface Announcement { id: number; urgent?: boolean; title: string; tag: string; by: string; date: string; body: string; ack: boolean }

export const initialAnnouncements = (): Announcement[] => [
  { id: 1, urgent: true, title: 'Maintenance du core banking — samedi 22h à 02h', tag: 'Urgent', by: "Direction des Systèmes d'Information", date: "Aujourd'hui, 08:00",
    body: 'Les opérations en agence seront suspendues pendant la fenêtre de maintenance. Merci d\'informer vos clients et de clôturer vos caisses avant 21h30.', ack: false },
  { id: 2, title: 'Lancement du pilote EximPhone', tag: 'Projet', by: 'Direction Générale', date: 'Hier, 15:30',
    body: 'À partir de lundi, le siège bascule sur EximPhone : un seul numéro interne sur votre poste de bureau et votre PC. Les agences suivront au prochain trimestre.', ack: false },
  { id: 3, title: 'Rappel : formation anti-blanchiment (AML) obligatoire', tag: 'Conformité', by: 'Département Conformité', date: 'Lun., 09:00',
    body: 'Le module e-learning AML doit être complété avant le 30 novembre. Contactez le poste 2210 pour toute question.', ack: true },
];
