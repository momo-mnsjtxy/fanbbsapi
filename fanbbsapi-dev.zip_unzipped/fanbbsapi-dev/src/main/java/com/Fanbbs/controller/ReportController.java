package com.Fanbbs.controller;

import com.Fanbbs.common.JWT;
import com.Fanbbs.common.PageList;
import com.Fanbbs.common.ResultAll;
import com.Fanbbs.common.baseFull;
import com.Fanbbs.entity.Article;
import com.Fanbbs.entity.Inbox;
import com.Fanbbs.entity.Report;
import com.Fanbbs.entity.Users;
import com.Fanbbs.service.ArticleService;
import com.Fanbbs.service.InboxService;
import com.Fanbbs.service.ReportService;
import com.Fanbbs.service.UsersService;
import com.alibaba.fastjson.JSONObject;
import com.auth0.jwt.interfaces.DecodedJWT;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.*;

import javax.servlet.http.HttpServletRequest;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

@Controller
@RequestMapping(value = "/report")
public class ReportController {
    @Autowired
    ReportService reportService;
    @Autowired
    ArticleService articleService;
    @Autowired
    InboxService inboxService;
    @Autowired
    UsersService usersService;

    ResultAll Result = new ResultAll();
    baseFull baseFull = new baseFull();

    @PostMapping(value = "/add")
    @ResponseBody
    public String add(@RequestParam(value = "type") String type,
                      @RequestParam(value = "reported", required = false) Integer reported,
                      @RequestParam(value = "article_id", required = false) Integer article_id,
                      @RequestParam(value = "reason") String reason,
                      HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在！", null);
            System.out.println(type);
            if (type.isEmpty()) return Result.getResultJson(202, "请选择类型！", null);
            if (type.equals("user") && reported.toString().isEmpty())
                return Result.getResultJson(202, "请选择要举报的用户！", null);
            if (type.equals("article") && (article_id == null || article_id.equals("")))
                return Result.getResultJson(203, "请选择要举报的帖子！", null);
            if (reason.isEmpty()) return Result.getResultJson(204, "请填写举报理由！", null);

            if (type.equals("user") && user.getUid() == reported)
                return Result.getResultJson(205, "你无法举报自己", null);
            // 帖子是否存在
            Article article = new Article();
            if (type.equals("article") && article_id != null) {
                article = articleService.selectByKey(article_id);
                if (article == null || article.getCid() == null) return Result.getResultJson(206, "帖子不存在！", null);
            }
            Users reportedUser = new Users();
            if (type.equals("user") && reported != null) {
                reportedUser = usersService.selectByKey(reported);
                if (reportedUser == null || reportedUser.getUid() == null)
                    return Result.getResultJson(206, "用户不存在！", null);
            }


            // 新建对象
            Report report = new Report();
            // 生成ticket
            long timeStamp = System.currentTimeMillis();
            SimpleDateFormat dateFormat = new SimpleDateFormat("yyyyMMdd");
            String ticket = dateFormat.format(timeStamp) + timeStamp / 1000 + user.getUid();
            // 设置数据
            report.setReported(reported);
            report.setType(type);
            report.setFlag(0);
            report.setReason(reason);
            report.setInformant(user.getUid());
            report.setTicket(Long.parseLong(ticket));
            report.setCreated((int) (timeStamp / 1000));
            reportService.insert(report);
            // 给管理员发送站内信
            Users users = new Users();
            users.setGroup("administrator");
            List<Users> adminUsers = usersService.selectList(users);
            if (!adminUsers.isEmpty()) {
                for (Users _users : adminUsers) {
                    Inbox inbox = new Inbox();
                    inbox.setCreated((int) (timeStamp / 1000));
                    inbox.setType("system");
                    inbox.setTouid(_users.getUid());
                    inbox.setText(String.format(
                            "收到来自用户ID：%s，%s的举报。举报%s【%s】，理由：%s，请尽快处理",
                            user.getUid(),
                            user.getScreenName() != null ? user.getScreenName() : user.getName(),
                            type.equals("user") ? "用户" : "帖子",
                            type.equals("user") ?
                                    (reportedUser.getScreenName() != null ? reportedUser.getScreenName() : reportedUser.getName()) :
                                    article.getTitle(), reason
                    ));
                    inboxService.insert(inbox);
                }
            }
            return Result.getResultJson(200, "举报成功！", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    // 删除
    @PostMapping(value = "/delete")
    @ResponseBody
    public String delete(RequestBody id,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            if (!permission(user)) return Result.getResultJson(202, "无权限", null);

            reportService.delete(id);
            return Result.getResultJson(200, "删除成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @RequestMapping(value = "/list")
    @ResponseBody
    public String list(@RequestParam(value = "page", defaultValue = "1", required = false) Integer page,
                       @RequestParam(value = "limit", defaultValue = "10", required = false) Integer limit,
                       @RequestParam(value = "searchKey", required = false) String searchKey,
                       @RequestParam(value = "order", required = false, defaultValue = "created desc") String order,
                       HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            if (!permission(user)) return Result.getResultJson(202, "无权限", null);
            // 查询数据
            PageList<Report> pageList = reportService.selectPage(new Report(), page, limit, searchKey, order);
            List<Report> reportList = pageList.getList();
            List<Object> dataList = new ArrayList<>();
            for (Report _report : reportList) {
                Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(_report), Map.class);
                // 查询被举报用户信息和举报人信息
                Map<String, Object> reported = new HashMap<>();
                // 如果是举报用户
                if (_report.getType().equals("user")) {
                    Users reportUser = usersService.selectByKey(_report.getReported());
                    reported.put("name", reportUser.getName());
                    reported.put("screenName", reportUser.getScreenName());
                    reported.put("uid", String.valueOf(reportUser.getUid()));
                    data.put("reported", reported);
                }
                // 如果是帖子
                if (_report.getType().equals("article")) {
                    Article article = articleService.selectByKey(_report.getArticle_id());
                    if (article != null) {
                        Map<String, Object> dataArticle = new HashMap<>();
                        dataArticle.put("title", article.getTitle());
                        dataArticle.put("text", baseFull.toStrByChinese(article.getText()));
                        dataArticle.put("authorId", article.getAuthorId());
                        Users author = usersService.selectByKey(article.getAuthorId());
                        if (author != null) {
                            Map<String, Object> dataAuthor = JSONObject.parseObject(JSONObject.toJSONString(author), Map.class);
                            dataAuthor.remove("email");
                            dataAuthor.remove("phone");
                            dataAuthor.remove("password");
                            dataArticle.put("authorInfo", dataAuthor);
                        }
                        data.put("article", dataArticle);
                    }

                }
                // 举报人信息
                Map<String, Object> Informant = new HashMap<>();
                Users informantUser = usersService.selectByKey(_report.getInformant());
                Informant.put("name", informantUser.getName());
                Informant.put("screenName", informantUser.getScreenName());
                Informant.put("uid", String.valueOf(informantUser.getUid()));
                // 加入信息
                data.put("informant", Informant);
                dataList.add(data);
            }
            Map<String, Object> data = new HashMap<>();
            data.put("total", pageList.getTotalCount());
            data.put("count", reportList.size());
            data.put("data", dataList);
            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    @PostMapping(value = "/action")
    @ResponseBody
    public String action(@RequestParam(value = "id") Integer id,
                         @RequestParam(value = "num", defaultValue = "1") Integer num,
                         @RequestParam(value = "receipt", required = false) String receipt,
                         HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);

            if (user == null || user.getUid() == null) {
                return Result.getResultJson(201, "用户不存在", null);
            }

            if (!permission(user)) {
                return Result.getResultJson(202, "无权限", null);
            }

            Report report = reportService.selectByKey(id);
            if (report == null) {
                return Result.getResultJson(203, "工单不存在！", null);
            }

            long currentTime = System.currentTimeMillis() / 1000;
            long banDuration = 86400L * num; // 封禁时长，单位秒
            // 通知举报人
            Inbox inbox = new Inbox();
            inbox.setTouid(report.getInformant());
            inbox.setIsread(0);
            inbox.setType("system");
            inbox.setCreated((int) currentTime);

            if (report.getType().equals("user")) {
                Users reported = usersService.selectByKey(report.getReported());
                if (reported == null || reported.getUid() == null) {
                    return Result.getResultJson(201, "封禁用户不存在", null);
                }

                if (reported.getBantime() == null || reported.getBantime() < currentTime) {
                    reported.setBantime((int) (currentTime + banDuration));
                } else {
                    reported.setBantime(reported.getBantime() + (int) banDuration);
                }
                inbox.setText(String.format("感谢您的举报，我们已经对您举报的用户：%s进行了封禁处理。再次感谢您对社区和谐做出的贡献。",
                        reported.getScreenName() != null ? reported.getScreenName() : reported.getName()));

                usersService.update(reported);

            } else {
                Article article = articleService.selectByKey(report.getArticle_id());
                article.setStatus("lock");
                inbox.setText(String.format("感谢您的举报，我们已经对您举报的帖子：%s进行了锁帖处理。再次感谢您对社区和谐做出的贡献。",
                        article.getTitle()));
                articleService.update(article);
            }
            // 更新数据
            report.setFlag(1);
            report.setReceipt(receipt);
            report.setProcessed(user.getUid());
            reportService.update(report);
            inboxService.insert(inbox);
            return Result.getResultJson(200, "操作完成", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }


    private Users getUser(String token) {
        if (token == null || token.isEmpty()) return new Users();
        // 获取用户信息
        DecodedJWT verify = JWT.verify(token);
        Users user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
        return user;
    }

    private boolean permission(Users user) {
        if (user.getUid() == null || user.getUid().equals(0))
            return false;
        if (user.getGroup().equals("administrator") || user.getGroup().equals("editor"))
            return true;
        return false;
    }
}
