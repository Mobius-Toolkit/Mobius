use dioxus::prelude::*;

#[component]
pub fn Shell() -> Element {
    rsx! {
        header { class: "shell-header", "Mobius" }
    }
}
