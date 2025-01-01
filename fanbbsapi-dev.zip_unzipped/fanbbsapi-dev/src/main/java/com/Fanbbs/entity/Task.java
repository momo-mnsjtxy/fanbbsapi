package com.Fanbbs.entity;

import lombok.Data;

import java.io.Serializable;

@Data
public class Task implements Serializable {
    private static final long serialVersionUID = 1L;
    private Integer id;
    private Integer day_point;
    private Integer day_exp;
    private Integer comment_point;
    private Integer comment_exp;
    private Integer comment_times;
    private Integer publish_point;
    private Integer publish_exp;
    private Integer publish_times;
    private Integer share_point;
    private Integer share_exp;
    private Integer share_times;
    private Integer like_point;
    private Integer like_exp;
    private Integer like_times;
    private Integer mark_point;
    private Integer mark_exp;
    private Integer mark_times;
    private Integer view_point;
    private Integer view_exp;
    private Integer view_times;
    private Integer acc_sign;
    private Integer acc_exp;
    private Integer acc_point;
    private Double punish;


}
