fn main() {
    if let Err(error) = candace_csf::run(std::env::args_os()) {
        if let Some(parse_error) = error.downcast_ref::<clap::Error>() {
            parse_error.exit();
        }
        eprintln!("[FAIL] {error:#}");
        std::process::exit(1);
    }
}
