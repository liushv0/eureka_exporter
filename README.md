## 自述
适用于eureka的exporter, 将其上注册的应用状态转化为Prometheus指标; 

可同时监控多个eureka集群;

支持basic auth鉴权;


## 启动参数
```text 
config.file --> 配置文件路径,配置文件格式参考 config.yaml 
```

## 使用说明
在eureka core 1.4版本对应的server(v1.2)上验证过, 默认指标暴露地址 `http://eureka-exporter:9109/metrics`;

## k8s部署
参考k8s-deploy.yaml

### 单点eureka与集群eureka
参考配置文件,多节点的eureka 集群配置多个URL即可;

参考 `config.yaml`

### 指标标签
eureka_instance --> 传入的eureka name, 用于区分多个eureka集群

eureka_application --> 注册在eureka上的应用名

eureka_app_hostname --> eureka注册信息中的hostname 字段

eureka_app_status --> eureka注册状态字段

eureka_url       --> 配置的某个eurekaURL, 一个eureka_instance可以对应多个eureka_url

extend_labels --> config.yaml 中可以配置多个扩展标签, 从metadata中取对应的key/value值作为指标的标签


### 指标值
1
