package main

/*
#cgo pkg-config: gtk+-3.0 ayatana-appindicator3-0.1

#include <stdlib.h>
#include <string.h>
#include <gtk/gtk.h>
#include <libayatana-appindicator/app-indicator.h>

static GtkWidget *win;
static GtkWidget *statusIcon;
static GtkWidget *titleLabel;
static GtkWidget *detailLabel;
static GtkWidget *progress;
static GtkWidget *hotkeyBtn;
static GtkWidget *serverEntry;
static GtkWidget *micCombo;
static GtkWidget *pages;
static GtkWidget *helpBtn;
static GtkWidget *helpText;
static GtkWidget *asrCombo;
static GtkWidget *cleanupCombo;
static GtkWidget *languageCombo;
static GtkWidget *secondEntry;
static char asr_good[256];
static char san_good[256];
static char asr_typed[256];
static char san_typed[256];
static GtkWidget *startBtn;
static GtkWidget *stopBtn;
static GtkWidget *restartBtn;
static GtkWidget *saveBtn;
static GtkWidget *startItem;
static GtkWidget *stopItem;
static AppIndicator *tray;
static int capturing;
static int dirty_key;
static int ui_up;
static char key_name[64];
static char trayTitle[96];

extern void patterGoSave(char *server, char *hotkey, char *asr, char *cleanup, char *language, char *second, char *mic);
extern void patterGoStart(void);
extern void patterGoStop(void);
extern void patterGoRestart(void);
extern void patterGoQuit(void);
extern void patterGoPoke(void);

static void remember_key(const char *name) {
    if (!name || !name[0]) {
        name = "F9";
    }
    snprintf(key_name, sizeof key_name, "%s", name);
}

static void show_key_on_button(void) {
    if (hotkeyBtn && !capturing) {
        gtk_button_set_label(GTK_BUTTON(hotkeyBtn), key_name);
    }
}

static void cancel_capture(void) {
    capturing = 0;
    if (hotkeyBtn) {
        gtk_style_context_remove_class(gtk_widget_get_style_context(hotkeyBtn), "capturing");
    }
    show_key_on_button();
}

static gboolean poke_idle(gpointer data) {
    (void)data;
    patterGoPoke();
    return G_SOURCE_REMOVE;
}

static gboolean present_idle(gpointer data) {
    (void)data;
    if (!win) {
        return G_SOURCE_REMOVE;
    }
    cancel_capture();
    gtk_widget_show(win);
    gtk_window_deiconify(GTK_WINDOW(win));
    gtk_window_present(GTK_WINDOW(win));
    return G_SOURCE_REMOVE;
}

static void patter_ui_present(void) {
    if (!ui_up) {
        return;
    }
    g_idle_add(present_idle, NULL);
}

static gboolean on_delete(GtkWidget *w, GdkEvent *e, gpointer p) {
    (void)e;
    (void)p;
    cancel_capture();
    gtk_widget_hide(w);
    return TRUE;
}

static gboolean on_state(GtkWidget *w, GdkEventWindowState *e, gpointer p) {
    (void)p;
    if (e->new_window_state & GDK_WINDOW_STATE_ICONIFIED) {
        cancel_capture();
        gtk_window_deiconify(GTK_WINDOW(w));
        gtk_widget_hide(w);
    }
    return FALSE;
}

static void on_show_item(GtkMenuItem *item, gpointer p) {
    (void)item;
    (void)p;
    g_idle_add(present_idle, NULL);
}

static void on_quit_item(GtkMenuItem *item, gpointer p) {
    (void)item;
    (void)p;
    patterGoQuit();
}

static void on_start(GtkWidget *w, gpointer p) {
    (void)w;
    (void)p;
    patterGoStart();
}

static void on_stop(GtkWidget *w, gpointer p) {
    (void)w;
    (void)p;
    patterGoStop();
}

static void show_help_text(void) {
    char *markup;
    if (!helpText) {
        return;
    }
    markup = g_markup_printf_escaped(
        "<b>Talk</b>  Hold %s, speak, let go. Words land while you hold. The line is cleaned when you release.\n\n"
        "<b>Fix</b>  Say the correction in the same breath: \"Tuesday, no, Wednesday\" types Wednesday.\n\n"
        "<b>Undo</b>  Say \"scratch that\" on its own to remove the last line.\n\n"
        "<b>Names</b>  One name per line in glossary.txt beside server.py. They are typed as written there.\n\n"
        "<b>Server</b>  server.py runs where the GPU is. Put its address in Server, and the same token in ~/.config/patter/token.\n\n"
        "<b>Models</b>  Pick one, or type a Hugging Face id. A new model downloads on Save and this panel shows the progress.\n\n"
        "<b>Trouble</b>  The log is ~/.local/state/patter/daemon.log.",
        key_name);
    gtk_label_set_markup(GTK_LABEL(helpText), markup);
    g_free(markup);
}

static void on_help(GtkToggleButton *b, gpointer p) {
    (void)p;
    if (gtk_toggle_button_get_active(b)) {
        show_help_text();
        gtk_stack_set_visible_child_name(GTK_STACK(pages), "help");
    } else {
        gtk_stack_set_visible_child_name(GTK_STACK(pages), "settings");
    }
}

static void on_restart(GtkWidget *w, gpointer p) {
    (void)w;
    (void)p;
    patterGoRestart();
}

static void on_hotkey(GtkButton *b, gpointer p) {
    (void)b;
    (void)p;
    capturing = 1;
    gtk_style_context_add_class(gtk_widget_get_style_context(hotkeyBtn), "capturing");
    gtk_button_set_label(GTK_BUTTON(hotkeyBtn), "Press a key");
}

static gboolean on_key(GtkWidget *w, GdkEventKey *e, gpointer p) {
    guint mods;
    guint kv;
    const gchar *name;
    (void)w;
    (void)p;
    if (!capturing) {
        return FALSE;
    }
    if (e->type != GDK_KEY_PRESS) {
        return TRUE;
    }
    if (e->keyval == GDK_KEY_Escape) {
        cancel_capture();
        return TRUE;
    }
    if (e->is_modifier) {
        return TRUE;
    }
    mods = e->state & (GDK_CONTROL_MASK | GDK_MOD1_MASK | GDK_SUPER_MASK | GDK_MOD4_MASK);
    if (mods) {
        return TRUE;
    }
    kv = gdk_keyval_to_upper(e->keyval);
    name = gdk_keyval_name(kv);
    if (!name || !name[0]) {
        return TRUE;
    }
    remember_key(name);
    dirty_key = 1;
    cancel_capture();
    return TRUE;
}

static GtkWidget *combo_entry(GtkWidget *combo) {
    return gtk_bin_get_child(GTK_BIN(combo));
}

static int good_source(const char *s) {
    if (!s || !s[0] || strlen(s) > 200) {
        return 0;
    }
    if (strchr(s, ' ') || strchr(s, '\n') || strchr(s, '\t')) {
        return 0;
    }
    return strchr(s, '/') != NULL;
}

// A list pick returns its id. Typed text is a Hugging Face id or a path.
// Empty text means the last good id. Anything else returns NULL.
static const char *picked(GtkWidget *combo, char *typed, const char *fallback) {
    const char *id = gtk_combo_box_get_active_id(GTK_COMBO_BOX(combo));
    const char *raw;
    int n;
    if (id && id[0]) {
        return id;
    }
    raw = gtk_entry_get_text(GTK_ENTRY(combo_entry(combo)));
    while (*raw == ' ') {
        raw++;
    }
    snprintf(typed, 256, "%s", raw);
    n = (int)strlen(typed);
    while (n > 0 && typed[n - 1] == ' ') {
        typed[--n] = 0;
    }
    if (!typed[0]) {
        return fallback;
    }
    return good_source(typed) ? typed : NULL;
}

static void mark_bad(GtkWidget *combo, int bad) {
    GtkStyleContext *c = gtk_widget_get_style_context(combo_entry(combo));
    if (bad) {
        gtk_style_context_add_class(c, "error");
    } else {
        gtk_style_context_remove_class(c, "error");
    }
}

static void on_model_changed(GtkComboBox *combo, gpointer data) {
    const char *id = gtk_combo_box_get_active_id(combo);
    (void)data;
    mark_bad(GTK_WIDGET(combo), 0);
    if (id && id[0]) {
        snprintf(GTK_WIDGET(combo) == asrCombo ? asr_good : san_good, sizeof asr_good, "%s", id);
    }
}

static void on_save(GtkButton *b, gpointer p) {
    const char *asr;
    const char *san;
    (void)b;
    (void)p;
    if (capturing) {
        return;
    }
    asr = picked(asrCombo, asr_typed, asr_good);
    san = picked(cleanupCombo, san_typed, san_good);
    mark_bad(asrCombo, asr == NULL);
    mark_bad(cleanupCombo, san == NULL);
    if (!asr || !san) {
        gtk_widget_grab_focus(combo_entry(asr ? cleanupCombo : asrCombo));
        return;
    }
    patterGoSave(
        (char *)gtk_entry_get_text(GTK_ENTRY(serverEntry)),
        key_name,
        (char *)asr,
        (char *)san,
        (char *)(gtk_combo_box_get_active_id(GTK_COMBO_BOX(languageCombo)) ?: "English"),
        (char *)gtk_entry_get_text(GTK_ENTRY(secondEntry)),
        (char *)(gtk_combo_box_get_active_id(GTK_COMBO_BOX(micCombo)) ?: ""));
}

static const char *status_class(const char *title) {
    if (!title) {
        return "off";
    }
    if (strcmp(title, "Ready") == 0) {
        return "ok";
    }
    if (strcmp(title, "Listening") == 0 || strcmp(title, "Correcting") == 0
        || strcmp(title, "Downloading") == 0 || strcmp(title, "Loading") == 0) {
        return "listen";
    }
    if (strcmp(title, "Stopped") == 0) {
        return "off";
    }
    return "warn";
}

static void set_status_class(const char *name) {
    GtkStyleContext *c = gtk_widget_get_style_context(statusIcon);
    const char *names[] = {"ok", "listen", "warn", "off"};
    int i;
    for (i = 0; i < 4; i++) {
        gtk_style_context_remove_class(c, names[i]);
    }
    gtk_style_context_add_class(c, name);
}

static GtkWidget *kicker(const char *text) {
    GtkWidget *l = gtk_label_new(text);
    gtk_widget_set_name(l, "kicker");
    gtk_widget_set_halign(l, GTK_ALIGN_CENTER);
    return l;
}

static void choose_id(GtkComboBoxText *box, const char *id) {
    if (id && id[0] && gtk_combo_box_set_active_id(GTK_COMBO_BOX(box), id)) {
        return;
    }
    if (!id || !id[0]) {
        gtk_combo_box_set_active(GTK_COMBO_BOX(box), 0);
        return;
    }
    gtk_combo_box_text_prepend(box, id, id);
    gtk_combo_box_set_active_id(GTK_COMBO_BOX(box), id);
}

static GtkWidget *model_combo(int speech, const char *current) {
    GtkWidget *w = gtk_combo_box_text_new_with_entry();
    GtkComboBoxText *box = GTK_COMBO_BOX_TEXT(w);
    GtkWidget *entry = combo_entry(w);
    gtk_widget_set_name(w, "choice");
    gtk_widget_set_hexpand(w, TRUE);
    if (speech) {
        gtk_combo_box_text_append(box, "Qwen/Qwen3-ASR-1.7B", "Qwen3 ASR 1.7B");
        gtk_combo_box_text_append(box, "Qwen/Qwen3-ASR-0.6B", "Qwen3 ASR 0.6B");
    } else {
        gtk_combo_box_text_append(box, "Qwen/Qwen3-4B-Instruct-2507", "Qwen3 4B Instruct");
        gtk_combo_box_text_append(box, "Qwen/Qwen2.5-3B-Instruct", "Qwen2.5 3B Instruct");
    }
    choose_id(box, current);
    gtk_widget_set_name(entry, "compact");
    gtk_entry_set_placeholder_text(GTK_ENTRY(entry), "org/model or /path");
    gtk_widget_set_tooltip_text(w, "Pick a model, or type a Hugging Face id or a local path. A model that is not on the server yet is fetched the next time the server starts.");
    g_signal_connect(w, "changed", G_CALLBACK(on_model_changed), NULL);
    return w;
}

// mics is "name\x1fdescription\x1e" per source. Empty id is the default.
static GtkWidget *mic_combo(const char *mics, const char *current) {
    GtkWidget *w = gtk_combo_box_text_new();
    GtkComboBoxText *box = GTK_COMBO_BOX_TEXT(w);
    char *copy = g_strdup(mics ? mics : "");
    char *p = copy;
    gtk_widget_set_name(w, "choice");
    gtk_widget_set_hexpand(w, TRUE);
    gtk_combo_box_text_append(box, "", "System default");
    while (p && *p) {
        char *end = strchr(p, '\x1e');
        char *sep;
        if (end) {
            *end = 0;
        }
        sep = strchr(p, '\x1f');
        if (sep) {
            *sep = 0;
            gtk_combo_box_text_append(box, p, sep[1] ? sep + 1 : p);
        }
        p = end ? end + 1 : NULL;
    }
    g_free(copy);
    if (!current || !current[0] || !gtk_combo_box_set_active_id(GTK_COMBO_BOX(w), current)) {
        if (current && current[0]) {
            gtk_combo_box_text_prepend(box, current, current);
        }
        gtk_combo_box_set_active_id(GTK_COMBO_BOX(w), current && current[0] ? current : "");
    }
    gtk_widget_set_tooltip_text(w, "An input that is unplugged falls back to the system default.");
    return w;
}

static GtkWidget *language_combo(const char *current) {
    static const char *langs[] = {
        "English", "Chinese", "French", "German", "Spanish",
        "Portuguese", "Japanese", "Korean", "Russian", "Italian", "Arabic", NULL
    };
    GtkWidget *w = gtk_combo_box_text_new();
    int i;
    gtk_widget_set_name(w, "choice");
    gtk_widget_set_hexpand(w, TRUE);
    for (i = 0; langs[i]; i++) {
        gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(w), langs[i], langs[i]);
    }
    choose_id(GTK_COMBO_BOX_TEXT(w), current && current[0] ? current : "English");
    return w;
}

static GtkSizeGroup *labelGroup;
static GtkSizeGroup *rowGroup;

static void add_setting_row(GtkGrid *grid, int row, const char *name, GtkWidget *control) {
    GtkWidget *label = gtk_label_new(name);
    gtk_widget_set_name(label, "row");
    gtk_widget_set_halign(label, GTK_ALIGN_START);
    gtk_widget_set_valign(label, GTK_ALIGN_CENTER);
    gtk_widget_set_valign(control, GTK_ALIGN_CENTER);
    gtk_size_group_add_widget(labelGroup, label);
    gtk_size_group_add_widget(rowGroup, control);
    gtk_grid_attach(grid, label, 0, row, 1, 1);
    gtk_grid_attach(grid, control, 1, row, 1, 1);
}

static GtkWidget *icon_button(const char *icon, const char *tip, GCallback cb) {
    GtkWidget *b = gtk_button_new();
    GtkWidget *img = gtk_image_new_from_icon_name(icon, GTK_ICON_SIZE_LARGE_TOOLBAR);
    gtk_image_set_pixel_size(GTK_IMAGE(img), 20);
    gtk_container_add(GTK_CONTAINER(b), img);
    gtk_widget_set_tooltip_text(b, tip);
    gtk_style_context_add_class(gtk_widget_get_style_context(b), "action");
    g_signal_connect(b, "clicked", cb, NULL);
    return b;
}

static int patter_ui_run(const char *hotkey, const char *server, const char *asr, const char *cleanup, const char *language, const char *second, const char *mics, const char *mic) {
    GtkWidget *root;
    GtkWidget *body;
    GtkWidget *hero;
    GtkWidget *grid;
    GtkWidget *rule;
    GtkWidget *actions;
    GtkWidget *left;
    GtkWidget *right;
    GtkWidget *menu;
    GtkWidget *showItem;
    GtkWidget *quitItem;
    GtkWidget *sep;
    GtkCssProvider *css;

    if (!gtk_init_check(NULL, NULL)) {
        return 1;
    }
    remember_key(hotkey);

    win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_widget_set_name(win, "patter");
    gtk_window_set_title(GTK_WINDOW(win), "Patter");
    gtk_window_set_resizable(GTK_WINDOW(win), FALSE);
    gtk_window_set_position(GTK_WINDOW(win), GTK_WIN_POS_CENTER);
    gtk_window_set_icon_name(GTK_WINDOW(win), "audio-input-microphone");
    gtk_container_set_border_width(GTK_CONTAINER(win), 24);
    g_signal_connect(win, "delete-event", G_CALLBACK(on_delete), NULL);
    g_signal_connect(win, "window-state-event", G_CALLBACK(on_state), NULL);
    g_signal_connect(win, "key-press-event", G_CALLBACK(on_key), NULL);

    css = gtk_css_provider_new();
    gtk_css_provider_load_from_data(css,
        "window#patter { background-color: @window_bg_color; }\n"
        "label#title { font-size: 26px; font-weight: 700; }\n"
        "label#detail { font-size: 13px; }\n"
        "label#kicker { font-size: 11px; letter-spacing: 1.5px; color: alpha(@window_fg_color, 0.55); }\n"
        "#status { -gtk-icon-style: symbolic; }\n"
        "#status.ok { color: @success_color; }\n"
        "#status.listen { color: @accent_color; }\n"
        "#status.warn { color: @warning_color; }\n"
        "#status.off { color: alpha(@window_fg_color, 0.38); }\n"
        "button#hotkey {\n"
        "  font-family: \"Fira Mono\";\n"
        "  font-size: 28px;\n"
        "  font-weight: 700;\n"
        "  min-width: 168px;\n"
        "  min-height: 84px;\n"
        "  border-radius: 22px;\n"
        "  background-image: none;\n"
        "  background-color: @card_bg_color;\n"
        "  border: 1px solid alpha(@window_fg_color, 0.08);\n"
        "  box-shadow: none;\n"
        "}\n"
        "button#hotkey:hover {\n"
        "  background-color: alpha(@window_fg_color, 0.10);\n"
        "  border-color: alpha(@window_fg_color, 0.16);\n"
        "}\n"
        "button#hotkey.capturing {\n"
        "  font-size: 15px;\n"
        "  font-weight: 600;\n"
        "  color: @accent_color;\n"
        "  border-color: @accent_color;\n"
        "}\n"
        "progressbar#progress trough, progressbar#progress progress { min-height: 4px; border-radius: 2px; }\n"
        "label#help { font-size: 13px; }\n"
        "label#row { font-size: 13px; color: alpha(@window_fg_color, 0.75); }\n"
        "entry#compact, combobox#choice entry, combobox#choice button {\n"
        "  min-height: 34px;\n"
        "  border-radius: 10px;\n"
        "  padding: 2px 12px;\n"
        "  font-size: 13px;\n"
        "  box-shadow: none;\n"
        "}\n"
        "combobox#choice entry { border-top-right-radius: 0; border-bottom-right-radius: 0; }\n"
        "combobox#choice button { border-top-left-radius: 0; border-bottom-left-radius: 0; padding: 2px 6px; }\n"
        "button.action {\n"
        "  min-height: 40px;\n"
        "  min-width: 56px;\n"
        "  border-radius: 14px;\n"
        "  padding: 6px 14px;\n"
        "  -gtk-icon-style: symbolic;\n"
        "}\n",
        -1, NULL);
    gtk_style_context_add_provider_for_screen(gdk_screen_get_default(),
        GTK_STYLE_PROVIDER(css), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);

    labelGroup = gtk_size_group_new(GTK_SIZE_GROUP_HORIZONTAL);
    rowGroup = gtk_size_group_new(GTK_SIZE_GROUP_VERTICAL);

    root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 20);
    gtk_container_add(GTK_CONTAINER(win), root);

    body = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 28);
    gtk_box_pack_start(GTK_BOX(root), body, TRUE, TRUE, 0);

    hero = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    gtk_widget_set_size_request(hero, 232, -1);
    gtk_widget_set_valign(hero, GTK_ALIGN_CENTER);
    gtk_box_pack_start(GTK_BOX(body), hero, FALSE, FALSE, 0);

    statusIcon = gtk_image_new_from_icon_name("emblem-ok-symbolic", GTK_ICON_SIZE_DIALOG);
    gtk_image_set_pixel_size(GTK_IMAGE(statusIcon), 44);
    gtk_widget_set_name(statusIcon, "status");
    gtk_widget_set_halign(statusIcon, GTK_ALIGN_CENTER);
    set_status_class("ok");
    gtk_box_pack_start(GTK_BOX(hero), statusIcon, FALSE, FALSE, 0);

    titleLabel = gtk_label_new("Ready");
    gtk_widget_set_name(titleLabel, "title");
    gtk_widget_set_halign(titleLabel, GTK_ALIGN_CENTER);
    gtk_box_pack_start(GTK_BOX(hero), titleLabel, FALSE, FALSE, 0);

    detailLabel = gtk_label_new("Hold the hotkey and speak.");
    gtk_widget_set_name(detailLabel, "detail");
    gtk_label_set_line_wrap(GTK_LABEL(detailLabel), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(detailLabel), 26);
    gtk_label_set_justify(GTK_LABEL(detailLabel), GTK_JUSTIFY_CENTER);
    gtk_widget_set_halign(detailLabel, GTK_ALIGN_CENTER);
    gtk_style_context_add_class(gtk_widget_get_style_context(detailLabel), "dim-label");
    gtk_box_pack_start(GTK_BOX(hero), detailLabel, FALSE, FALSE, 0);

    progress = gtk_progress_bar_new();
    gtk_widget_set_name(progress, "progress");
    gtk_widget_set_no_show_all(progress, TRUE);
    gtk_widget_set_size_request(progress, 168, -1);
    gtk_widget_set_halign(progress, GTK_ALIGN_CENTER);
    gtk_widget_set_margin_top(progress, 6);
    gtk_box_pack_start(GTK_BOX(hero), progress, FALSE, FALSE, 0);

    hotkeyBtn = gtk_button_new_with_label(key_name);
    gtk_widget_set_name(hotkeyBtn, "hotkey");
    gtk_widget_set_halign(hotkeyBtn, GTK_ALIGN_CENTER);
    gtk_widget_set_margin_top(hotkeyBtn, 14);
    g_signal_connect(hotkeyBtn, "clicked", G_CALLBACK(on_hotkey), NULL);
    gtk_box_pack_start(GTK_BOX(hero), hotkeyBtn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(hero), kicker("HOTKEY"), FALSE, FALSE, 0);

    gtk_box_pack_start(GTK_BOX(body), gtk_separator_new(GTK_ORIENTATION_VERTICAL), FALSE, FALSE, 0);

    pages = gtk_stack_new();
    gtk_stack_set_homogeneous(GTK_STACK(pages), TRUE);
    gtk_stack_set_transition_type(GTK_STACK(pages), GTK_STACK_TRANSITION_TYPE_CROSSFADE);
    gtk_box_pack_start(GTK_BOX(body), pages, TRUE, TRUE, 0);
    grid = gtk_grid_new();
    gtk_grid_set_column_spacing(GTK_GRID(grid), 16);
    gtk_grid_set_row_spacing(GTK_GRID(grid), 10);
    gtk_widget_set_size_request(grid, 400, -1);
    gtk_widget_set_valign(grid, GTK_ALIGN_CENTER);
    gtk_stack_add_named(GTK_STACK(pages), grid, "settings");
    helpText = gtk_label_new("");
    gtk_widget_set_name(helpText, "help");
    gtk_label_set_line_wrap(GTK_LABEL(helpText), TRUE);
    gtk_label_set_xalign(GTK_LABEL(helpText), 0);
    gtk_widget_set_valign(helpText, GTK_ALIGN_CENTER);
    gtk_label_set_max_width_chars(GTK_LABEL(helpText), 60);
    gtk_stack_add_named(GTK_STACK(pages), helpText, "help");

    serverEntry = gtk_entry_new();
    gtk_widget_set_name(serverEntry, "compact");
    gtk_widget_set_hexpand(serverEntry, TRUE);
    gtk_entry_set_text(GTK_ENTRY(serverEntry), server ? server : "");
    gtk_entry_set_placeholder_text(GTK_ENTRY(serverEntry), "http://127.0.0.1:8765");
    secondEntry = gtk_entry_new();
    gtk_widget_set_name(secondEntry, "compact");
    gtk_widget_set_hexpand(secondEntry, TRUE);
    gtk_entry_set_text(GTK_ENTRY(secondEntry), second ? second : "");
    gtk_entry_set_placeholder_text(GTK_ENTRY(secondEntry), "optional");
    gtk_widget_set_tooltip_text(secondEntry, "Used only when the server does not connect.");
    asrCombo = model_combo(1, asr);
    cleanupCombo = model_combo(0, cleanup);
    languageCombo = language_combo(language);
    snprintf(asr_good, sizeof asr_good, "%s", gtk_combo_box_get_active_id(GTK_COMBO_BOX(asrCombo)) ?: "Qwen/Qwen3-ASR-1.7B");
    snprintf(san_good, sizeof san_good, "%s", gtk_combo_box_get_active_id(GTK_COMBO_BOX(cleanupCombo)) ?: "Qwen/Qwen3-4B-Instruct-2507");
    micCombo = mic_combo(mics, mic);
    add_setting_row(GTK_GRID(grid), 0, "Microphone", micCombo);
    add_setting_row(GTK_GRID(grid), 1, "Server", serverEntry);
    add_setting_row(GTK_GRID(grid), 2, "Second", secondEntry);
    rule = gtk_separator_new(GTK_ORIENTATION_HORIZONTAL);
    gtk_widget_set_margin_top(rule, 4);
    gtk_widget_set_margin_bottom(rule, 4);
    gtk_grid_attach(GTK_GRID(grid), rule, 0, 3, 2, 1);
    add_setting_row(GTK_GRID(grid), 4, "Speech", asrCombo);
    add_setting_row(GTK_GRID(grid), 5, "Cleanup", cleanupCombo);
    add_setting_row(GTK_GRID(grid), 6, "Language", languageCombo);

    actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_pack_start(GTK_BOX(root), actions, FALSE, FALSE, 0);
    left = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    right = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_box_pack_start(GTK_BOX(actions), left, FALSE, FALSE, 0);
    gtk_box_pack_end(GTK_BOX(actions), right, FALSE, FALSE, 0);
    startBtn = icon_button("media-playback-start-symbolic", "Start", G_CALLBACK(on_start));
    stopBtn = icon_button("media-playback-stop-symbolic", "Stop", G_CALLBACK(on_stop));
    helpBtn = gtk_toggle_button_new();
    gtk_container_add(GTK_CONTAINER(helpBtn), gtk_image_new_from_icon_name("help-about-symbolic", GTK_ICON_SIZE_LARGE_TOOLBAR));
    gtk_widget_set_tooltip_text(helpBtn, "Help");
    gtk_style_context_add_class(gtk_widget_get_style_context(helpBtn), "action");
    g_signal_connect(helpBtn, "toggled", G_CALLBACK(on_help), NULL);
    restartBtn = icon_button("view-refresh-symbolic", "Restart", G_CALLBACK(on_restart));
    saveBtn = icon_button("object-select-symbolic", "Save", G_CALLBACK(on_save));
    gtk_style_context_add_class(gtk_widget_get_style_context(saveBtn), "suggested-action");
    gtk_box_pack_start(GTK_BOX(left), startBtn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(left), stopBtn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(right), helpBtn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(right), restartBtn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(right), saveBtn, FALSE, FALSE, 0);

    menu = gtk_menu_new();
    showItem = gtk_menu_item_new_with_label("Show Patter");
    startItem = gtk_menu_item_new_with_label("Start");
    stopItem = gtk_menu_item_new_with_label("Stop");
    sep = gtk_separator_menu_item_new();
    quitItem = gtk_menu_item_new_with_label("Quit");
    g_signal_connect(showItem, "activate", G_CALLBACK(on_show_item), NULL);
    g_signal_connect(startItem, "activate", G_CALLBACK(on_start), NULL);
    g_signal_connect(stopItem, "activate", G_CALLBACK(on_stop), NULL);
    g_signal_connect(quitItem, "activate", G_CALLBACK(on_quit_item), NULL);
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), showItem);
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), startItem);
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), stopItem);
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), sep);
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), quitItem);
    gtk_widget_show_all(menu);

    tray = app_indicator_new("patter", "emblem-ok-symbolic", APP_INDICATOR_CATEGORY_APPLICATION_STATUS);
    app_indicator_set_status(tray, APP_INDICATOR_STATUS_ACTIVE);
    app_indicator_set_title(tray, "Patter: Ready");
    app_indicator_set_menu(tray, GTK_MENU(menu));
    app_indicator_set_secondary_activate_target(tray, showItem);

    gtk_widget_show_all(win);
    ui_up = 1;
    g_idle_add(poke_idle, NULL);
    gtk_main();
    ui_up = 0;
    return 0;
}

typedef struct {
    char *icon;
    char *title;
    char *detail;
    char *hotkey;
    int armed;
    int percent;
} StatusMsg;

static gboolean apply_status(gpointer data) {
    StatusMsg *m = data;
    if (titleLabel) {
        gtk_image_set_from_icon_name(GTK_IMAGE(statusIcon), m->icon, GTK_ICON_SIZE_DIALOG);
        gtk_image_set_pixel_size(GTK_IMAGE(statusIcon), 44);
        set_status_class(status_class(m->title));
        gtk_label_set_text(GTK_LABEL(titleLabel), m->title);
        gtk_label_set_text(GTK_LABEL(detailLabel), m->detail);
        if (m->percent < 0) {
            gtk_widget_hide(progress);
        } else {
            if (m->percent >= 100) {
                gtk_progress_bar_pulse(GTK_PROGRESS_BAR(progress));
            } else {
                gtk_progress_bar_set_fraction(GTK_PROGRESS_BAR(progress), m->percent / 100.0);
            }
            gtk_widget_show(progress);
        }
        if (!capturing && !dirty_key) {
            remember_key(m->hotkey);
            show_key_on_button();
        }
        gtk_widget_set_sensitive(startBtn, !m->armed);
        gtk_widget_set_sensitive(stopBtn, m->armed);
        gtk_widget_set_sensitive(startItem, !m->armed);
        gtk_widget_set_sensitive(stopItem, m->armed);
        snprintf(trayTitle, sizeof trayTitle, "Patter: %s", m->title);
        app_indicator_set_icon_full(tray, m->icon, trayTitle);
        app_indicator_set_title(tray, trayTitle);
    }
    free(m->icon);
    free(m->title);
    free(m->detail);
    free(m->hotkey);
    free(m);
    return G_SOURCE_REMOVE;
}

static void patter_ui_post(char *icon, char *title, char *detail, char *hotkey, int armed, int percent) {
    StatusMsg *m = malloc(sizeof *m);
    if (!m) {
        free(icon);
        free(title);
        free(detail);
        free(hotkey);
        return;
    }
    m->icon = icon;
    m->title = title;
    m->detail = detail;
    m->hotkey = hotkey;
    m->armed = armed;
    m->percent = percent;
    g_idle_add(apply_status, m);
}
*/
import "C"
import (
	"os"
	"runtime"
	"strings"
	"unsafe"
)

var uiDaemon *daemon

func markerIcon(m marker) string {
	switch {
	case m.title == "Stopped":
		return "media-playback-pause-symbolic"
	case !m.ok && m.title == "Microphone":
		return "microphone-sensitivity-muted-symbolic"
	case !m.ok:
		return "dialog-warning-symbolic"
	case m.title == "Listening" || m.title == "Correcting":
		return "audio-input-microphone-symbolic"
	case m.title == "Downloading":
		return "folder-download-symbolic"
	case m.title == "Loading":
		return "emblem-synchronizing-symbolic"
	default:
		return "emblem-ok-symbolic"
	}
}

func pokeWindow() {
	d := uiDaemon
	if d == nil {
		return
	}
	m := d.marker()
	cfg := d.config()
	armed := C.int(0)
	if hotkeyArmed.Load() {
		armed = 1
	}
	C.patter_ui_post(
		C.CString(markerIcon(m)),
		C.CString(m.title),
		C.CString(m.detail),
		C.CString(cfg.Hotkey),
		armed,
		C.int(progressValue(m)),
	)
}

// progressValue is -1 for no bar, 100+ for a pulsing bar, else a percent.
func progressValue(m marker) int {
	switch m.title {
	case "Downloading":
		if m.percent < 0 {
			return 100
		}
		return m.percent
	case "Loading":
		return 100
	}
	return -1
}

func presentWindow() {
	C.patter_ui_present()
}

func showWindow() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

type marker struct {
	ok      bool
	title   string
	detail  string
	percent int
}

func shortModel(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func (d *daemon) marker() marker {
	cfg := d.config()
	key := cfg.Hotkey
	if key == "" {
		key = "F9"
	}
	hold := "Hold " + key + " and speak."
	switch {
	case !hotkeyArmed.Load():
		return marker{title: "Stopped", detail: "Dictation is stopped."}
	case !d.mic.flowing.Load():
		return marker{title: "Microphone", detail: "The microphone is not open."}
	case !d.serverOK.Load():
		h, _ := d.serverState.Load().(health)
		switch h.State {
		case "downloading":
			return marker{ok: true, title: "Downloading", detail: shortModel(h.Model), percent: h.Percent}
		case "loading":
			return marker{ok: true, title: "Loading", detail: shortModel(h.Model), percent: -1}
		case "failed":
			return marker{title: "Server", detail: "Could not load " + shortModel(h.Model) + "."}
		}
		return marker{title: "Server", detail: "The speech server is not answering."}
	case d.busy.Load():
		return marker{ok: true, title: "Correcting", detail: "Fixing the last phrase."}
	case d.keyHeld.Load() || d.recording.Load():
		return marker{ok: true, title: "Listening", detail: hold}
	default:
		return marker{ok: true, title: "Ready", detail: hold}
	}
}

func runStatus(d *daemon) {
	uiDaemon = d
	cfg := d.config()
	hot := C.CString(cfg.Hotkey)
	server := C.CString(cfg.Server)
	asr := C.CString(cfg.speechModel())
	cleanup := C.CString(cfg.cleanupModel())
	language := C.CString(cfg.speechLanguage())
	second := C.CString(cfg.Fallback)
	var list strings.Builder
	for _, src := range listSources() {
		list.WriteString(src.Name + "\x1f" + src.Desc + "\x1e")
	}
	mics := C.CString(list.String())
	micName := C.CString(cfg.Mic)
	defer C.free(unsafe.Pointer(mics))
	defer C.free(unsafe.Pointer(micName))
	defer C.free(unsafe.Pointer(hot))
	defer C.free(unsafe.Pointer(server))
	defer C.free(unsafe.Pointer(asr))
	defer C.free(unsafe.Pointer(cleanup))
	defer C.free(unsafe.Pointer(language))
	defer C.free(unsafe.Pointer(second))
	if C.patter_ui_run(hot, server, asr, cleanup, language, second, mics, micName) != 0 {
		logf("window failed")
		os.Exit(1)
	}
}

//export patterGoStart
func patterGoStart() {
	hotkeyArmed.Store(true)
	logf("started")
	pokeWindow()
}

//export patterGoStop
func patterGoStop() {
	hotkeyArmed.Store(false)
	logf("stopped")
	pokeWindow()
}

//export patterGoSave
func patterGoSave(server, hotkey, asr, cleanup, language, second, mic *C.char) {
	if uiDaemon == nil {
		return
	}
	cfg := uiDaemon.config()
	s := strings.TrimSpace(C.GoString(server))
	if s == "" {
		logf("save: server empty")
		return
	}
	cfg.Server = strings.TrimRight(s, "/")
	if k := strings.TrimSpace(C.GoString(hotkey)); k != "" {
		cfg.Hotkey = k
	}
	cfg.ASR = strings.TrimSpace(C.GoString(asr))
	cfg.Sanitizer = strings.TrimSpace(C.GoString(cleanup))
	cfg.Language = strings.TrimSpace(C.GoString(language))
	cfg.Fallback = strings.TrimRight(strings.TrimSpace(C.GoString(second)), "/")
	cfg.Mic = strings.TrimSpace(C.GoString(mic))
	cfg.ASR = cfg.speechModel()
	cfg.Sanitizer = cfg.cleanupModel()
	cfg.Language = cfg.speechLanguage()
	if err := saveConfig(cfg); err != nil {
		logf("save: %v", err)
		return
	}
	uiDaemon.replaceConfig(cfg)
	if err := pushSettings(cfg); err != nil {
		logf("settings: %v", err)
	}
	logf("saved")
	restartWhenIdle()
}

//export patterGoRestart
func patterGoRestart() {
	logf("restart")
	restartWhenIdle()
}

//export patterGoPoke
func patterGoPoke() {
	pokeWindow()
}

//export patterGoQuit
func patterGoQuit() {
	logf("quit")
	os.Exit(0)
}
