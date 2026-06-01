/*
 * Java Spring Boot pipeline example.
 *
 * Responsibilities:
 *   - Expose /health for deployment health checks.
 *   - Expose /info with language and app metadata.
 *
 * Boundaries:
 *   - Does not depend on external services.
 *   - Does not require a reverse proxy.
 */
package dev.superdev.example;

import java.util.Map;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@RestController
public class ExampleApplication {
  public static void main(String[] args) {
    SpringApplication.run(ExampleApplication.class, args);
  }

  @GetMapping("/health")
  public String health() {
    return "ok";
  }

  @GetMapping("/info")
  public Map<String, String> info() {
    return Map.of(
        "app", "java-springboot",
        "language", "java",
        "version", System.getenv().getOrDefault("APP_VERSION", ""));
  }

  @GetMapping("/")
  public String index() {
    return "java-springboot";
  }
}
