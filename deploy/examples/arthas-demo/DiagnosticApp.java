package demo;

import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;

/** Bounded diagnostic fixtures only; no external service dependencies. */
public class DiagnosticApp {
    private static volatile long result;
    public static String slowOperation() throws InterruptedException {
        TimeUnit.MILLISECONDS.sleep(350);
        return "slow operation: 350ms\n";
    }
    public static String failingOperation() {
        throw new IllegalStateException("intentional diagnostic-demo exception");
    }
    public static String cpuOperation() {
        long until = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(500);
        long value = 1;
        while (System.nanoTime() < until) value = value * 1664525L + 1013904223L;
        result = value;
        return "bounded CPU sample completed\n";
    }
    public static void main(String[] args) throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 16);
        server.createContext("/", exchange -> {
            int status = 200;
            String response;
            try {
                switch (exchange.getRequestURI().getPath()) {
                    case "/health": response = "ok\n"; break;
                    case "/slow": response = slowOperation(); break;
                    case "/error": response = failingOperation(); break;
                    case "/cpu": response = cpuOperation(); break;
                    default: response = "FTC Arthas demo: /health /slow /error /cpu\n";
                }
            } catch (Exception e) {
                status = 500;
                response = e.toString() + "\n";
            }
            byte[] bytes = response.getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(status, bytes.length);
            exchange.getResponseBody().write(bytes);
            exchange.close();
        });
        server.setExecutor(Executors.newFixedThreadPool(4));
        server.start();
        // Lightweight repeatable samples make finite trace/watch commands testable.
        Executors.newSingleThreadScheduledExecutor(r -> new Thread(r, "diagnostic-demo-sampler")).scheduleWithFixedDelay(() -> {
            try { slowOperation(); } catch (Exception ignored) { }
            try { failingOperation(); } catch (Exception ignored) { }
        }, 5, 5, TimeUnit.SECONDS);
        System.out.println("Arthas demo ready: demo.DiagnosticApp on :8080");
    }
}
