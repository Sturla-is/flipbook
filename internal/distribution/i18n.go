package distribution

import (
	"regexp"
	"strings"
)

var languageTagPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

func Language(value string) string {
	value = strings.TrimSpace(value)
	if !languageTagPattern.MatchString(value) {
		return "en"
	}
	return value
}

func Direction(language string) string {
	base := strings.ToLower(strings.Split(Language(language), "-")[0])
	switch base {
	case "ar", "arc", "dv", "fa", "he", "iw", "ku", "ps", "ur", "yi":
		return "rtl"
	default:
		return "ltr"
	}
}

func Strings(language string) map[string]string {
	base := strings.ToLower(strings.Split(Language(language), "-")[0])
	result := make(map[string]string, len(viewerStrings["en"]))
	for key, value := range viewerStrings["en"] {
		result[key] = value
	}
	if translated, ok := viewerStrings[base]; ok {
		for key, value := range translated {
			result[key] = value
		}
	}
	return result
}

var viewerStrings = map[string]map[string]string{
	"en": {
		"previous": "Previous page", "next": "Next page", "page": "Page", "of": "of",
		"single": "Single page", "double": "Two pages", "continuous": "Continuous", "text_mode": "Text mode",
		"zoom_out": "Zoom out", "zoom_in": "Zoom in", "fit_width": "Fit width", "reset_zoom": "Reset zoom",
		"search": "Search", "search_placeholder": "Search this publication", "no_results": "No results",
		"text_unavailable": "Text is not available for this publication.", "results": "results",
		"contents": "Contents", "pages": "Pages", "bookmarks": "Bookmarks", "bookmark": "Bookmark this page",
		"bookmark_added": "Bookmark added", "bookmark_removed": "Bookmark removed", "no_bookmarks": "No bookmarks yet.",
		"download": "Download PDF", "print": "Print current page", "share": "Share", "embed": "Embed",
		"copy": "Copy", "copied": "Copied", "close": "Close", "native_share": "Share using device",
		"email": "Email", "qr_code": "QR code", "download_svg": "Download SVG", "download_png": "Download PNG",
		"share_page": "Include current page", "embed_preview": "Responsive embed code",
		"lead_title": "Continue reading", "lead_email": "Email address", "lead_submit": "Continue",
		"lead_dismiss": "Not now", "lead_consent": "I agree to be contacted about this publication.",
		"purchase_title": "Purchase to continue", "purchase_submit": "Buy now", "loading": "Loading…",
		"password_title": "This publication is protected", "password": "Password", "unlock": "Unlock",
		"error": "Something went wrong. Please try again.", "read_online": "Read this publication",
		"start": "Start page", "toolbar": "Toolbar", "branding": "Branding", "background": "Background",
		"width": "Width", "height": "Height", "aspect_ratio": "Aspect ratio", "options": "Options",
		"previous_result": "Previous result", "next_result": "Next result", "video": "Play video",
		"end_cta": "Continue", "resume": "Resume", "fullscreen": "Fullscreen", "reading_mode": "Reading mode",
	},
	"es": {
		"previous": "Página anterior", "next": "Página siguiente", "page": "Página", "of": "de",
		"single": "Una página", "double": "Dos páginas", "continuous": "Continuo", "text_mode": "Modo texto",
		"zoom_out": "Reducir", "zoom_in": "Ampliar", "fit_width": "Ajustar al ancho", "reset_zoom": "Restablecer zoom",
		"search": "Buscar", "search_placeholder": "Buscar en esta publicación", "no_results": "Sin resultados",
		"text_unavailable": "El texto no está disponible para esta publicación.", "results": "resultados",
		"contents": "Contenido", "pages": "Páginas", "bookmarks": "Marcadores", "bookmark": "Marcar esta página",
		"download": "Descargar PDF", "print": "Imprimir página actual", "share": "Compartir", "embed": "Insertar",
		"copy": "Copiar", "copied": "Copiado", "close": "Cerrar", "native_share": "Compartir con el dispositivo",
		"email": "Correo", "qr_code": "Código QR", "download_svg": "Descargar SVG", "download_png": "Descargar PNG",
		"lead_title": "Sigue leyendo", "lead_email": "Correo electrónico", "lead_submit": "Continuar",
		"lead_dismiss": "Ahora no", "purchase_title": "Compra para continuar", "purchase_submit": "Comprar ahora",
		"password": "Contraseña", "unlock": "Desbloquear", "error": "Algo salió mal. Inténtalo de nuevo.",
	},
	"fr": {
		"previous": "Page précédente", "next": "Page suivante", "page": "Page", "of": "sur",
		"single": "Une page", "double": "Deux pages", "continuous": "Continu", "text_mode": "Mode texte",
		"zoom_out": "Zoom arrière", "zoom_in": "Zoom avant", "fit_width": "Ajuster à la largeur", "reset_zoom": "Réinitialiser le zoom",
		"search": "Rechercher", "search_placeholder": "Rechercher dans cette publication", "no_results": "Aucun résultat",
		"text_unavailable": "Le texte n’est pas disponible pour cette publication.", "results": "résultats",
		"contents": "Sommaire", "pages": "Pages", "bookmarks": "Signets", "bookmark": "Ajouter cette page aux signets",
		"download": "Télécharger le PDF", "print": "Imprimer la page actuelle", "share": "Partager", "embed": "Intégrer",
		"copy": "Copier", "copied": "Copié", "close": "Fermer", "native_share": "Partager via l’appareil",
		"email": "E-mail", "qr_code": "Code QR", "download_svg": "Télécharger SVG", "download_png": "Télécharger PNG",
		"lead_title": "Continuer la lecture", "lead_email": "Adresse e-mail", "lead_submit": "Continuer",
		"lead_dismiss": "Pas maintenant", "purchase_title": "Acheter pour continuer", "purchase_submit": "Acheter",
		"password": "Mot de passe", "unlock": "Déverrouiller", "error": "Une erreur est survenue. Veuillez réessayer.",
	},
	"de": {
		"previous": "Vorherige Seite", "next": "Nächste Seite", "page": "Seite", "of": "von",
		"single": "Eine Seite", "double": "Zwei Seiten", "continuous": "Fortlaufend", "text_mode": "Textmodus",
		"zoom_out": "Verkleinern", "zoom_in": "Vergrößern", "fit_width": "An Breite anpassen", "reset_zoom": "Zoom zurücksetzen",
		"search": "Suchen", "search_placeholder": "In dieser Publikation suchen", "no_results": "Keine Ergebnisse",
		"text_unavailable": "Für diese Publikation ist kein Text verfügbar.", "results": "Ergebnisse",
		"contents": "Inhalt", "pages": "Seiten", "bookmarks": "Lesezeichen", "bookmark": "Diese Seite merken",
		"download": "PDF herunterladen", "print": "Aktuelle Seite drucken", "share": "Teilen", "embed": "Einbetten",
		"copy": "Kopieren", "copied": "Kopiert", "close": "Schließen", "native_share": "Mit Gerät teilen",
		"email": "E-Mail", "qr_code": "QR-Code", "download_svg": "SVG herunterladen", "download_png": "PNG herunterladen",
		"lead_title": "Weiterlesen", "lead_email": "E-Mail-Adresse", "lead_submit": "Weiter",
		"lead_dismiss": "Nicht jetzt", "purchase_title": "Zum Weiterlesen kaufen", "purchase_submit": "Jetzt kaufen",
		"password": "Passwort", "unlock": "Entsperren", "error": "Etwas ist schiefgelaufen. Bitte erneut versuchen.",
	},
	"pt": {
		"previous": "Página anterior", "next": "Próxima página", "page": "Página", "of": "de",
		"single": "Uma página", "double": "Duas páginas", "continuous": "Contínuo", "text_mode": "Modo texto",
		"search": "Pesquisar", "search_placeholder": "Pesquisar nesta publicação", "no_results": "Sem resultados",
		"contents": "Conteúdo", "pages": "Páginas", "bookmarks": "Marcadores", "download": "Baixar PDF",
		"share": "Compartilhar", "embed": "Incorporar", "copy": "Copiar", "copied": "Copiado", "close": "Fechar",
		"lead_email": "Endereço de e-mail", "lead_submit": "Continuar", "purchase_submit": "Comprar agora",
	},
	"it": {
		"previous": "Pagina precedente", "next": "Pagina successiva", "page": "Pagina", "of": "di",
		"single": "Pagina singola", "double": "Due pagine", "continuous": "Continuo", "text_mode": "Modalità testo",
		"search": "Cerca", "search_placeholder": "Cerca in questa pubblicazione", "no_results": "Nessun risultato",
		"contents": "Indice", "pages": "Pagine", "bookmarks": "Segnalibri", "download": "Scarica PDF",
		"share": "Condividi", "embed": "Incorpora", "copy": "Copia", "copied": "Copiato", "close": "Chiudi",
	},
	"nl": {
		"previous": "Vorige pagina", "next": "Volgende pagina", "page": "Pagina", "of": "van",
		"single": "Eén pagina", "double": "Twee pagina's", "continuous": "Doorlopend", "text_mode": "Tekstmodus",
		"search": "Zoeken", "search_placeholder": "Zoeken in deze publicatie", "no_results": "Geen resultaten",
		"contents": "Inhoud", "pages": "Pagina's", "bookmarks": "Bladwijzers", "download": "PDF downloaden",
		"share": "Delen", "embed": "Insluiten", "copy": "Kopiëren", "copied": "Gekopieerd", "close": "Sluiten",
	},
	"ar": {
		"previous": "الصفحة السابقة", "next": "الصفحة التالية", "page": "صفحة", "of": "من",
		"single": "صفحة واحدة", "double": "صفحتان", "continuous": "مستمر", "text_mode": "وضع النص",
		"zoom_out": "تصغير", "zoom_in": "تكبير", "fit_width": "ملاءمة العرض", "reset_zoom": "إعادة ضبط التكبير",
		"search": "بحث", "search_placeholder": "ابحث في هذا المنشور", "no_results": "لا توجد نتائج",
		"text_unavailable": "النص غير متاح لهذا المنشور.", "results": "نتائج",
		"contents": "المحتويات", "pages": "الصفحات", "bookmarks": "الإشارات المرجعية", "bookmark": "أضف إشارة مرجعية",
		"download": "تنزيل PDF", "print": "طباعة الصفحة الحالية", "share": "مشاركة", "embed": "تضمين",
		"copy": "نسخ", "copied": "تم النسخ", "close": "إغلاق", "native_share": "مشاركة عبر الجهاز",
		"email": "البريد الإلكتروني", "qr_code": "رمز QR", "download_svg": "تنزيل SVG", "download_png": "تنزيل PNG",
		"lead_title": "متابعة القراءة", "lead_email": "البريد الإلكتروني", "lead_submit": "متابعة",
		"lead_dismiss": "ليس الآن", "purchase_title": "اشترِ للمتابعة", "purchase_submit": "اشترِ الآن",
		"password": "كلمة المرور", "unlock": "فتح", "error": "حدث خطأ ما. حاول مرة أخرى.",
	},
	"he": {
		"previous": "העמוד הקודם", "next": "העמוד הבא", "page": "עמוד", "of": "מתוך",
		"single": "עמוד אחד", "double": "שני עמודים", "continuous": "רציף", "text_mode": "מצב טקסט",
		"zoom_out": "הקטנה", "zoom_in": "הגדלה", "fit_width": "התאמה לרוחב", "reset_zoom": "איפוס תקריב",
		"search": "חיפוש", "search_placeholder": "חיפוש בפרסום", "no_results": "אין תוצאות",
		"text_unavailable": "טקסט אינו זמין לפרסום זה.", "results": "תוצאות",
		"contents": "תוכן", "pages": "עמודים", "bookmarks": "סימניות", "bookmark": "הוספת סימנייה",
		"download": "הורדת PDF", "print": "הדפסת העמוד הנוכחי", "share": "שיתוף", "embed": "הטמעה",
		"copy": "העתקה", "copied": "הועתק", "close": "סגירה", "native_share": "שיתוף באמצעות המכשיר",
		"email": "דואר אלקטרוני", "qr_code": "קוד QR", "download_svg": "הורדת SVG", "download_png": "הורדת PNG",
		"lead_title": "המשך קריאה", "lead_email": "כתובת דואר אלקטרוני", "lead_submit": "המשך",
		"lead_dismiss": "לא עכשיו", "purchase_title": "רכישה להמשך", "purchase_submit": "קנייה",
		"password": "סיסמה", "unlock": "פתיחה", "error": "משהו השתבש. נסה שוב.",
	},
}
