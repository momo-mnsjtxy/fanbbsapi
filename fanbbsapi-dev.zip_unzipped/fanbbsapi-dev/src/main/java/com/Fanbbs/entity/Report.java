package com.Fanbbs.entity;

import java.io.Serializable;

import lombok.Data;

@Data
public class Report implements Serializable {
    private static final long serialVersionUID = 1L;
    // ID
    private Integer id;
    // 单号
    private Long ticket;
    // 类型
    private String type;
    // 举报人
    private Integer informant;
    // 被举报人
    private Integer reported;
    //帖子ID
    private Integer article_id;
    // 理由
    private String reason;
    // 处理标志
    private Integer flag;
    // 回执
    private String receipt;
    // 处理人
    private Integer processed;
    // 创建时间
    private Integer created;
}
