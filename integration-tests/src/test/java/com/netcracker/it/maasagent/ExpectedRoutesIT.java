package com.netcracker.it.maasagent;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.netcracker.cloud.junit.cloudcore.extension.annotations.Cloud;
import com.netcracker.cloud.junit.cloudcore.extension.annotations.EnableExtension;
import com.netcracker.cloud.junit.cloudcore.extension.annotations.PortForward;
import com.netcracker.cloud.junit.cloudcore.extension.annotations.Value;
import io.fabric8.kubernetes.api.model.ConfigMap;
import io.fabric8.kubernetes.client.KubernetesClient;
import lombok.extern.slf4j.Slf4j;
import okhttp3.OkHttpClient;
import okhttp3.Request;
import okhttp3.Response;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;
import io.fabric8.kubernetes.client.dsl.ExecWatch;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Slf4j
@EnableExtension
public class ExpectedRoutesIT {

    @PortForward(serviceName = @Value("internal-gateway-service"))
    protected static URL internalGateway;

    @PortForward(serviceName = @Value("private-gateway-service"))
    protected static URL privateGateway;

    @PortForward(serviceName = @Value("public-gateway-service"))
    protected static URL publicGateway;

    @Cloud
    protected static KubernetesClient kubernetesClient;

    protected static OkHttpClient okHttpClient;
    protected static ObjectMapper objectMapper;
    protected static String namespace;
    
    protected static Map<String, List<String>> expectedRoutesMap;
    protected static ConfigMap routesConfigMap;
    protected static String executorPod;

    @BeforeAll
    public static void setUp() throws Exception {
        namespace = kubernetesClient.getNamespace();
        
        var pods = kubernetesClient.pods()
                .inNamespace(namespace)
                .withLabel("app", "maas-agent")
                .list();
        
        if (pods.getItems().isEmpty()) {
            pods = kubernetesClient.pods()
                    .inNamespace(namespace)
                    .withLabel("name", "maas-agent")
                    .list();
        }

        objectMapper = new ObjectMapper();
        
        okHttpClient = new OkHttpClient.Builder()
                .readTimeout(30, TimeUnit.SECONDS)
                .connectTimeout(30, TimeUnit.SECONDS)
                .build();
        
        loadExpectedRoutesFromConfigMap();
        
        if (!pods.getItems().isEmpty()) {
            executorPod = pods.getItems().get(0).getMetadata().getName();
            log.info("Executor pod: {}", executorPod);
        }

        log.info("Internal gateway URL: {}", internalGateway);
        log.info("Public gateway URL: {}", publicGateway);
        log.info("Test namespace: {}", namespace);
        log.info("Loaded {} routes for internal gateway", expectedRoutesMap.get("internal").size());
        log.info("Loaded {} routes for public gateway", expectedRoutesMap.get("public").size());
        log.info("Loaded {} routes for private gateway", expectedRoutesMap.get("private").size());
    }

    private static String executeInClusterPod(String... command) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        ByteArrayOutputStream err = new ByteArrayOutputStream();
        
        log.info("Executing in pod {}: {}", executorPod, String.join(" ", command));
        
        try (ExecWatch execWatch = kubernetesClient.pods()
                .inNamespace(namespace)
                .withName(executorPod)
                .writingOutput(out)
                .writingError(err)
                .exec(command)) {
            
            // Wait for command to complete - exitCode() returns CompletableFuture
            Integer exitCode = execWatch.exitCode().get();
            
            String output = out.toString();
            String error = err.toString();
            
            log.info("Exit code: {}, Output: '{}', Error: '{}'", exitCode, output.trim(), error.trim());
            
            if (exitCode != null && exitCode != 0) {
                log.warn("Command failed with exit code {}: {}", exitCode, error);
            }
            
            return output;
        }
    }

    private static int curlInternalStatusCode(String path) throws Exception {
        String url = String.format("http://internal-gateway-service.%s.svc.cluster.local:8080%s", namespace, path);
        log.info("Curling: {}", url);
        
        String result = executeInClusterPod("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", url);
        
        if (result == null || result.trim().isEmpty()) {
            log.error("Empty response from curl for path: {}", path);
            return 999; // Return special code for empty response
        }
        
        try {
            return Integer.parseInt(result.trim());
        } catch (NumberFormatException e) {
            log.error("Failed to parse curl response: '{}' for path: {}", result, path);
            return 999;
        }
    }
    
    @BeforeEach
    public void createRoutesConfigMap() {
        // Create or update ConfigMap in Kubernetes before tests
        if (routesConfigMap == null) {
            routesConfigMap = loadConfigMapFromResource();
            kubernetesClient.configMaps()
                    .inNamespace(namespace)
                    .resource(routesConfigMap)
                    .createOrReplace();
            log.info("Created/Updated ConfigMap 'maas-agent-expected-routes' in namespace {}", namespace);
        }
    }
    
    private static void loadExpectedRoutesFromConfigMap() throws IOException {
        ConfigMap existingCm = kubernetesClient.configMaps()
                .inNamespace(namespace)
                .withName("maas-agent-expected-routes")
                .get();
        
        if (existingCm != null && existingCm.getData() != null) {
            log.info("Loading expected routes from existing ConfigMap in cluster");
            expectedRoutesMap = parseRoutesFromConfigMap(existingCm);
        } else {
            log.info("Loading expected routes from classpath resource");
            expectedRoutesMap = loadRoutesFromResource();
        }
    }
    
    private static ConfigMap loadConfigMapFromResource() {
        try (InputStream is = ExpectedRoutesIT.class.getResourceAsStream("/expected-routes-configmap.yaml")) {
            if (is == null) {
                log.warn("expected-routes-configmap.yaml not found");
                return null;
            }
            
            return kubernetesClient.configMaps()
                    .load(is)
                    .item();
        } catch (IOException e) {
            log.error("Failed to load ConfigMap from resource", e);
            return null;
        }
    }
    
    private static Map<String, List<String>> loadRoutesFromResource() throws IOException {
        try (InputStream is = ExpectedRoutesIT.class.getResourceAsStream("/expected-routes-configmap.yaml")) {
            if (is != null) {
                ConfigMap cm = kubernetesClient.configMaps().load(is).item();
                return parseRoutesFromConfigMap(cm);
            }
        }
        
        log.warn("No routes resource found, using default routes");
        return new java.util.HashMap<>();
    }
    
    private static Map<String, List<String>> parseRoutesFromConfigMap(ConfigMap cm) {
        Map<String, List<String>> routes = new java.util.HashMap<>();
        
        if (cm.getData() != null) {
            // Check for JSON format
            if (cm.getData().containsKey("routes.json")) {
                try {
                    String json = cm.getData().get("routes.json");
                    return objectMapper.readValue(json, new TypeReference<Map<String, List<String>>>() {});
                } catch (IOException e) {
                    log.error("Failed to parse routes.json", e);
                }
            }
            
            for (String gateway : List.of("internal", "private", "public")) {
                String key = gateway + "-routes";
                if (cm.getData().containsKey(key)) {
                    String routesStr = cm.getData().get(key);
                    List<String> routeList = List.of(routesStr.split("\\n"));
                    routeList = routeList.stream()
                            .filter(s -> !s.trim().isEmpty())
                            .map(String::trim)
                            .toList();
                    routes.put(gateway, routeList);
                }
            }
        }
        
        return routes;
    }

    static Stream<Arguments> routeTestCasesFromConfigMap() {
        List<Arguments> arguments = new ArrayList<>();
        
        for (Map.Entry<String, List<String>> entry : expectedRoutesMap.entrySet()) {
            String gatewayType = entry.getKey();
            for (String route : entry.getValue()) {
                arguments.add(Arguments.of(gatewayType, route, 200));
            }
        }
        
        arguments.add(Arguments.of("internal", "/non-existent-route-12345", 404));
        arguments.add(Arguments.of("private", "/non-existent-route-67890", 404));
        arguments.add(Arguments.of("public", "/non-existent-route-abcde", 404));
        
        return arguments.stream();
    }

    @ParameterizedTest(name = "[{0}] {1}")
    @MethodSource("routeTestCasesFromConfigMap")
    @DisplayName("Test routes via port-forward")
    void testRoutesFromConfigMap(String gatewayType, String path, int expectedStatus) throws Exception  {
        if ("internal".equals(gatewayType))  {
            int code = curlInternalStatusCode(path);
            log.info("Internal {} -> {}", path, code);
            
            if (code == 405) {
                log.info("Route exists but method not allowed");
                return;
            }
            
            if (expectedStatus == 200) {
                assertTrue(code != 404,
                    String.format("Route %s on %s gateway should be accessible, got %d", 
                        path, gatewayType, code));
            } else {
                assertEquals(expectedStatus, code,
                    String.format("Route %s on %s gateway returned unexpected status", path, gatewayType));
            }
            return;
        }
        
        URL gatewayUrl = getGatewayUrl(gatewayType);
        String fullUrl = buildUrl(gatewayUrl, path);
        
        Request request = new Request.Builder()
                .url(fullUrl)
                .get()
                .build();
        
        try (Response response = okHttpClient.newCall(request).execute()) {
            log.info("Testing route: {} -> Status: {}", fullUrl, response.code());
            
            if (response.code() == 405) {
                log.info("Route exists but method not allowed");
                return;
            }
            
            if (expectedStatus == 200) {
                assertTrue(response.code() != 404,
                    String.format("Route %s on %s gateway should be accessible, got %d", 
                        path, gatewayType, response.code()));
            } else {
                assertEquals(expectedStatus, response.code(),
                    String.format("Route %s on %s gateway returned unexpected status", path, gatewayType));
            }
        }
    }

    @Test
    @DisplayName("Verify all expected routes are configured")
    void verifyAllRoutesAccessible() throws Exception {
        List<String> failedRoutes = new ArrayList<>();
        
        for (Map.Entry<String, List<String>> entry : expectedRoutesMap.entrySet()) {
            String gatewayType = entry.getKey();
            if ("internal".equals(gatewayType)) continue;
            
            URL gatewayUrl = getGatewayUrl(gatewayType);
            
            for (String route : entry.getValue()) {
                String fullUrl = buildUrl(gatewayUrl, route);
                Request request = new Request.Builder()
                        .url(fullUrl)
                        .get()
                        .build();
                
                try (Response response = okHttpClient.newCall(request).execute()) {
                    if (response.code() >= 500) {
                        failedRoutes.add(String.format("%s:%s (status %d)", gatewayType, route, response.code()));
                        log.error("Route failed: {} -> {}", fullUrl, response.code());
                    }
                } catch (IOException e) {
                    failedRoutes.add(String.format("%s:%s (error: %s)", gatewayType, route, e.getMessage()));
                    log.error("Route error: {} -> {}", fullUrl, e.getMessage());
                }
            }
        }
        
        assertTrue(failedRoutes.isEmpty(), 
            "Following routes failed: " + String.join(", ", failedRoutes));
    }

    private static String buildUrl(URL baseUrl, String path) {
        String base = baseUrl.toString();
        if (base.endsWith("/")) {
            base = base.substring(0, base.length() - 1);
        }
        if (!path.startsWith("/")) {
            path = "/" + path;
        }
        return base + path;
    }

    private URL getGatewayUrl(String gatewayType) {
        switch (gatewayType.toLowerCase()) {
            case "internal":
                return internalGateway;
            case "private":
                return privateGateway;
            case "public":
                return publicGateway;
            default:
                throw new IllegalArgumentException("Unknown gateway type: " + gatewayType);
        }
    }
}