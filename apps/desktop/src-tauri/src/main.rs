// Continuum desktop shell (FR-G17, FR-N07 M1): loads the shared web UI (apps/web) and starts the
// embedded Go Controller (+ local agent) as a sidecar. If a Controller is already listening
// (e.g. `make local-all`), the sidecar exits on bind failure and the UI uses the running one.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Mutex;
use tauri::Manager;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

struct Sidecar(Mutex<Option<CommandChild>>);

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(Sidecar(Mutex::new(None)))
        .setup(|app| {
            let data_dir = app.path().app_data_dir()?;
            std::fs::create_dir_all(&data_dir)?;
            // catalog must live on a local disk (SQLite WAL, NFR-A04)
            let db = format!("sqlite:///{}", data_dir.join("continuum.db").display());
            let addr = std::env::var("CONTROLLER_ADDR").unwrap_or_else(|_| "127.0.0.1:8080".into());
            match app.shell().sidecar("continuum-controller") {
                Ok(cmd) => {
                    let (mut rx, child) = cmd
                        .env("ENV", "desktop")
                        .env("DATABASE_URL", db)
                        .env("CONTROLLER_ADDR", addr)
                        .env("CONTINUUM_EXIT_WITH_PARENT", "1") // controller exits if this shell dies (kill -9, crash)
                        .spawn()?;
                    tauri::async_runtime::spawn(async move {
                        while let Some(ev) = rx.recv().await {
                            match ev {
                                CommandEvent::Stdout(l) | CommandEvent::Stderr(l) => {
                                    eprintln!("[controller] {}", String::from_utf8_lossy(&l).trim_end())
                                }
                                CommandEvent::Terminated(t) => eprintln!("[controller] exited: {:?}", t.code),
                                _ => {}
                            }
                        }
                    });
                    *app.state::<Sidecar>().0.lock().unwrap() = Some(child);
                }
                Err(e) => eprintln!("controller sidecar unavailable: {e} (using an external controller)"),
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Continuum desktop")
        .run(|app, event| {
            if let tauri::RunEvent::Exit = event {
                // stop the embedded controller; it pauses running jobs and keeps partial files for resume
                if let Some(child) = app.state::<Sidecar>().0.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        });
}
