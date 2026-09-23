use clap::Parser;

#[tokio::main]
async fn main() {
    let cli = mobius_cli::Cli::parse();
    let scope = mobius_cli::Scope::from_env();
    match mobius_cli::run(&cli, &scope).await {
        Ok(out) => println!("{out}"),
        Err(e) => {
            eprintln!("error: {e}");
            std::process::exit(e.exit_code());
        }
    }
}
