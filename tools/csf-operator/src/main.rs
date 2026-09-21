fn main() {
    if let Err(error) = candace_csf::run(std::env::args_os()) {
        eprintln!("{error:#}");
        std::process::exit(1);
    }
}
